package httpapi

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/legacyimport"
)

// backupFilenamePattern is deliberately an allowlist, not just
// filepath.Base normalization: every backup filename this codebase ever
// produces matches it exactly (see newBackupFilename), so any :filename
// path param that doesn't match is rejected before ever touching the
// filesystem - the strongest available defense against path traversal on
// the download/delete endpoints below. Millisecond precision (not just
// seconds) matters: two backups requested in quick succession must not
// collide on the same filename and silently overwrite one another.
var backupFilenamePattern = regexp.MustCompile(`^rapido_\d{8}T\d{6}\.\d{3}Z\.sql\.gz$`)

const backupTimestampLayout = "20060102T150405.000Z"

func newBackupFilename(t time.Time) string {
	return fmt.Sprintf("rapido_%sZ.sql.gz", t.UTC().Format("20060102T150405.000"))
}

// backupCreatedAt parses the timestamp embedded in a filename this codebase
// generated, rather than trusting filesystem mtime - mtime survives a copy/
// rsync poorly, while the filename's timestamp is authoritative because we
// are the only writer of these files.
func backupCreatedAt(filename string) (time.Time, error) {
	trimmed := filename[len("rapido_") : len(filename)-len(".sql.gz")]
	return time.Parse(backupTimestampLayout, trimmed)
}

type backupInfo struct {
	Filename  string    `json:"filename"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// listBackupsSorted returns every backup file in dir, newest first. A
// missing directory (nothing has been backed up yet) is not an error - it
// just yields an empty list, matching a fresh install's "no backups yet"
// state rather than requiring the directory to be pre-created out of band.
func listBackupsSorted(dir string) ([]backupInfo, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []backupInfo{}, nil
	}
	if err != nil {
		return nil, err
	}

	backups := make([]backupInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !backupFilenamePattern.MatchString(e.Name()) {
			continue
		}
		createdAt, err := backupCreatedAt(e.Name())
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backupInfo{Filename: e.Name(), SizeBytes: info.Size(), CreatedAt: createdAt})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].Filename > backups[j].Filename })
	return backups, nil
}

func (h *Handler) handleListBackups(c *gin.Context) {
	backups, err := listBackupsSorted(h.backupDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not list backups"})
		return
	}
	c.JSON(http.StatusOK, backups)
}

// dumpDatabaseFn is the shape of h.dumpDatabase - a replaceable field
// (defaulted to execPgDump by NewHandler) rather than a hardcoded method
// call, purely so tests can exercise handleCreateBackup's surrounding
// logic - listing, retention pruning, the HTTP response shape - against a
// trivial fake without needing the real pg_dump binary installed, exactly
// like internal/hostmetrics separates parseX (pure, tested everywhere)
// from readX (the real /proc read, only exercised on Linux). This isolates
// an OS subprocess call; it is not mocking business logic.
type dumpDatabaseFn func(ctx context.Context, w *os.File) error

// splitDatabasePassword moves the password out of a connection URL: pg_dump's and
// psql's arguments can be read by anyone who can list processes on the host, and
// PGPASSWORD is where those tools look for it instead.
func splitDatabasePassword(databaseURL string) (address, password string) {
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.User == nil {
		return databaseURL, ""
	}
	password, _ = parsed.User.Password()
	parsed.User = url.User(parsed.User.Username())
	return parsed.String(), password
}

// execPgDump is the real implementation: runs pg_dump against databaseURL,
// writing a gzip-compressed plain-SQL dump to w.
func execPgDump(ctx context.Context, databaseURL string, w *os.File) error {
	gz := gzip.NewWriter(w)
	address, password := splitDatabasePassword(databaseURL)
	cmd := exec.CommandContext(ctx, "pg_dump", "--dbname="+address,
		"--format=plain", "--no-owner", "--no-privileges", "--clean", "--if-exists")
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
	cmd.Stdout = gz
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		gz.Close()
		return fmt.Errorf("pg_dump: %w: %s", err, stderr.String())
	}
	return gz.Close()
}

func (h *Handler) handleCreateBackup(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	info, err := h.createBackupNow(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	h.pruneOldBackups()
	c.JSON(http.StatusOK, info)
}

// createBackupNow is handleCreateBackup's actual work, factored out so the
// restore flow below can take a real safety backup of the current database
// before ever touching it - the one genuine protection against "restored
// the wrong file" - without going through gin.Context/HTTP at all.
//
// It leaves pruning to handleCreateBackup. A safety backup that pushed out the
// oldest file could delete the very backup being restored, and a few failed
// restores in a row would rotate every good backup away.
//
// A backup holds every credential the panel has, the secret that signs its
// tokens among them, so nothing but the panel's own user may read it. It only
// gets its final name once complete: a dump killed halfway must never look like
// a backup someone could restore.
func (h *Handler) createBackupNow(ctx context.Context) (backupInfo, error) {
	if err := os.MkdirAll(h.backupDir, 0o700); err != nil {
		return backupInfo{}, fmt.Errorf("could not create the backup directory: %w", err)
	}
	removeStaleBackupScratch(h.backupDir)

	filename := newBackupFilename(time.Now())
	path := filepath.Join(h.backupDir, filename)
	scratch := filepath.Join(h.backupDir, backupScratchPrefix+filename)
	f, err := os.OpenFile(scratch, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return backupInfo{}, fmt.Errorf("could not create the backup file: %w", err)
	}

	dumpErr := h.dumpDatabase(ctx, f)
	syncErr := f.Sync()
	closeErr := f.Close()
	if dumpErr != nil {
		os.Remove(scratch)
		return backupInfo{}, fmt.Errorf("backup failed: %w", dumpErr)
	}
	if err := errors.Join(syncErr, closeErr); err != nil {
		os.Remove(scratch)
		return backupInfo{}, fmt.Errorf("could not finalize the backup file: %w", err)
	}
	if err := os.Rename(scratch, path); err != nil {
		os.Remove(scratch)
		return backupInfo{}, fmt.Errorf("could not finalize the backup file: %w", err)
	}

	stat, err := os.Stat(path)
	if err != nil {
		return backupInfo{}, fmt.Errorf("backup created but could not be read back: %w", err)
	}
	createdAt, _ := backupCreatedAt(filename)
	return backupInfo{Filename: filename, SizeBytes: stat.Size(), CreatedAt: createdAt}, nil
}

// backupScratchPrefix marks a file still being written: no backup filename can
// start with it, so listing, downloading and restoring never see one.
const backupScratchPrefix = ".partial-"

// backupUploadPrefix is the same for an uploaded file while it's handled.
const backupUploadPrefix = ".upload-"

// removeStaleBackupScratch clears what a killed process left behind. The age
// keeps it from touching a dump another request is writing right now.
func removeStaleBackupScratch(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), backupScratchPrefix) && !strings.HasPrefix(entry.Name(), backupUploadPrefix) {
			continue
		}
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > 6*time.Hour {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// pruneOldBackups keeps only the most recent h.backupKeep files, mirroring
// DB_BACKUP_KEEP in the current Python system. Filenames sort
// lexicographically in chronological order (the embedded timestamp is
// fixed-width and UTC), so no separate mtime-based sort is needed.
func (h *Handler) pruneOldBackups() {
	backups, err := listBackupsSorted(h.backupDir)
	if err != nil || h.backupKeep <= 0 || len(backups) <= h.backupKeep {
		return
	}
	for _, b := range backups[h.backupKeep:] {
		os.Remove(filepath.Join(h.backupDir, b.Filename))
	}
}

func (h *Handler) handleDownloadBackup(c *gin.Context) {
	filename := c.Param("filename")
	if !backupFilenamePattern.MatchString(filename) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "Invalid backup filename"})
		return
	}
	path := filepath.Join(h.backupDir, filename)
	if _, err := os.Stat(path); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Backup not found"})
		return
	}
	c.FileAttachment(path, filename)
}

// restoreDatabaseFn is the shape of h.restoreDatabase - the restore-side
// counterpart of dumpDatabaseFn above, replaceable for the same reason
// (tests exercise the surrounding maintenance-mode/safety-backup/response
// logic without needing the real psql binary installed).
type restoreDatabaseFn func(ctx context.Context, gz io.Reader) error

// pgDumpTrailer is the last thing pg_dump writes. Without it the file ends where
// the dump stopped, and psql would replace the database with that much of it.
const pgDumpTrailer = "-- PostgreSQL database dump complete"

// verifyCompleteDump reads a gzipped dump all the way through, before anything
// is touched: a truncated file only fails once psql has already got most of it,
// and whether psql then commits depends on where exactly it was cut.
func verifyCompleteDump(file io.ReadSeeker) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("the backup file is not a valid gzip stream: %w", err)
	}
	defer gzr.Close()
	tail := &tailWriter{limit: 4096}
	if _, err := io.Copy(tail, gzr); err != nil {
		return fmt.Errorf("the backup file is damaged or incomplete: %w", err)
	}
	if !bytes.Contains(tail.buf, []byte(pgDumpTrailer)) {
		return errors.New("the backup file is incomplete: pg_dump did not finish writing it")
	}
	_, err = file.Seek(0, io.SeekStart)
	return err
}

// tailWriter keeps the last limit bytes written to it.
type tailWriter struct {
	limit int
	buf   []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	if len(p) >= t.limit {
		t.buf = append(t.buf[:0], p[len(p)-t.limit:]...)
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.limit:]...)
	}
	return len(p), nil
}

// pgDumpHeader is what every dump execPgDump produces starts with (pg_dump
// always emits this comment first) - the one check handleRestoreUpload can
// make without a real Postgres instance to hand the file to: does this at
// least look like a Postgres dump, as opposed to a MySQL/SQLite backup from
// the old system (which this endpoint doesn't support converting yet).
const pgDumpHeader = "-- PostgreSQL database dump"

// execPsqlRestore is the real implementation: replaces the ENTIRE public
// schema and replays gz's (gzip-compressed) SQL against it, all inside one
// transaction. Postgres DDL is transactional, so DROP SCHEMA/CREATE SCHEMA is
// part of the same atomic unit as the actual restore - a failure anywhere rolls
// back to the exact pre-restore state, not a half-dropped one. The transaction is
// spelled out in the stream rather than left to psql's --single-transaction, which
// commits at the end of its input, however that end came about: here COMMIT is
// only sent once the whole dump has gone through, and without it psql just
// disconnects and Postgres rolls everything back.
//
// The dump comes from a file, possibly an uploaded one, and psql would run any
// shell command it finds in there (`\!`). \restrict turns every backslash command
// off for the session; only \unrestrict with this one random key turns them back
// on, and the file can't know that key.
func execPsqlRestore(ctx context.Context, databaseURL string, gz io.Reader) error {
	gzr, err := gzip.NewReader(gz)
	if err != nil {
		return fmt.Errorf("not a valid gzip stream: %w", err)
	}
	defer gzr.Close()

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("psql: %w", err)
	}
	preamble := "\\restrict " + hex.EncodeToString(key) + "\nBEGIN;\nDROP SCHEMA public CASCADE;\nCREATE SCHEMA public;\n"

	runCtx, abort := context.WithCancel(ctx)
	defer abort()
	address, password := splitDatabasePassword(databaseURL)
	cmd := exec.CommandContext(runCtx, "psql", "--dbname="+address,
		"--set", "ON_ERROR_STOP=1", "-q")
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("psql: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("psql: %w", err)
	}
	// copyDone (not a plain shared variable) matters here: cmd.Wait() only
	// waits for the process to exit and for Cmd's OWN internal stdout/
	// stderr-copying goroutines - it does not know about this goroutine,
	// which is ours. Reading a plain variable right after Wait() returns
	// would be a data race with no guarantee the write below has happened
	// yet (e.g. psql exiting early on a bad statement while this goroutine
	// is still mid-write) - a channel receive after Wait() gives a real
	// happens-before edge instead.
	copyDone := make(chan error, 1)
	go func() {
		defer stdin.Close()
		_, err := io.WriteString(stdin, preamble)
		if err == nil {
			err = copyDumpForRestrictedPsql(stdin, gzr)
		}
		if err == nil {
			_, err = io.WriteString(stdin, "\nCOMMIT;\n")
		}
		if err != nil {
			abort()
		}
		copyDone <- err
	}()
	waitErr := cmd.Wait()
	copyErr := <-copyDone
	if copyErr != nil {
		return fmt.Errorf("streaming restore data to psql: %w", copyErr)
	}
	if waitErr != nil {
		return fmt.Errorf("psql: %w: %s", waitErr, stderr.String())
	}
	return nil
}

// copyDumpForRestrictedPsql copies a dump, leaving out the \restrict and
// \unrestrict lines pg_dump itself writes: inside our own \restrict they'd be
// refused as the backslash commands they are. COPY data is passed through
// untouched, where such a line would be somebody's data.
func copyDumpForRestrictedPsql(dst io.Writer, src io.Reader) error {
	reader := bufio.NewReaderSize(src, 1<<20)
	atLineStart, inCopy := true, false
	for {
		chunk, err := reader.ReadSlice('\n')
		skip := false
		if atLineStart && len(chunk) > 0 {
			switch {
			case inCopy:
				inCopy = !bytes.HasPrefix(chunk, []byte("\\."))
			case bytes.HasPrefix(chunk, []byte("\\restrict ")) || bytes.HasPrefix(chunk, []byte("\\unrestrict ")):
				skip = true
			case bytes.HasPrefix(chunk, []byte("COPY ")) && bytes.HasSuffix(bytes.TrimRight(chunk, "\r\n"), []byte("FROM stdin;")):
				inCopy = true
			}
		}
		if len(chunk) > 0 {
			atLineStart = chunk[len(chunk)-1] == '\n'
			if !skip {
				if _, werr := dst.Write(chunk); werr != nil {
					return werr
				}
			}
		}
		switch {
		case err == nil || errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			return nil
		default:
			return err
		}
	}
}

type restoreResultDTO struct {
	SafetyBackup backupInfo `json:"safety_backup"`
	Detail       string     `json:"detail"`
}

// restoreFromReader is the shared core of both restore endpoints below:
// enter maintenance mode (always lifted via defer, even on panic/error),
// take a real safety backup of the current database first, then replace
// it wholesale with gz's content. The safety backup happens AFTER
// maintenance mode is entered so nothing can write to the database in the
// gap between "we captured the safety backup" and "we started
// overwriting" - otherwise a write landing in that gap would be silently
// lost by the restore with no backup covering it either.
func (h *Handler) restoreFromReader(ctx context.Context, gz io.ReadSeeker) (restoreResultDTO, error) {
	if err := verifyCompleteDump(gz); err != nil {
		return restoreResultDTO{}, err
	}
	restoreTx, err := h.store.Pool.Begin(ctx)
	if err != nil {
		return restoreResultDTO{}, err
	}
	defer restoreTx.Rollback(context.Background())
	var owns bool
	if err := restoreTx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(814720033)").Scan(&owns); err != nil || !owns {
		return restoreResultDTO{}, fmt.Errorf("another restore or active mutation is in progress; retry shortly")
	}
	lockID := uuid.NewString()
	locked, lockErr := h.store.Cache.Raw().SetNX(ctx, "rapido:restore-lock", lockID, 15*time.Minute).Result()
	if lockErr != nil || !locked {
		return restoreResultDTO{}, fmt.Errorf("another restore is active or restore lock unavailable")
	}
	defer h.store.Cache.Raw().Eval(context.Background(), `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{"rapido:restore-lock"}, lockID)
	if err := h.store.Cache.SetMaintenanceMode(ctx, true); err != nil {
		return restoreResultDTO{}, fmt.Errorf("could not enter maintenance mode: %w", err)
	}
	defer h.store.Cache.SetMaintenanceMode(context.Background(), false)

	safety, err := h.createBackupNow(ctx)
	if err != nil {
		return restoreResultDTO{}, fmt.Errorf("aborted before touching the database - could not take a safety backup first: %w", err)
	}

	if err := h.restoreDatabase(ctx, gz); err != nil {
		return restoreResultDTO{}, fmt.Errorf("restore failed after a safety backup (%s) was already taken - restore that backup to recover: %w", safety.Filename, err)
	}

	if h.afterRestore != nil {
		if err := h.afterRestore(ctx); err != nil {
			file, openErr := os.Open(filepath.Join(h.backupDir, safety.Filename))
			if openErr != nil {
				return restoreResultDTO{}, fmt.Errorf("post-restore migration failed and safety backup could not be opened: %w", err)
			}
			defer file.Close()
			if recoveryErr := h.restoreDatabase(ctx, file); recoveryErr != nil {
				return restoreResultDTO{}, fmt.Errorf("post-restore migration failed; automatic recovery also failed: %w", recoveryErr)
			}
			return restoreResultDTO{}, fmt.Errorf("backup schema could not be upgraded; original database restored: %w", err)
		}
	}
	// Every cached value (admin lookups, core config, node config, ...) is
	// definitionally stale the instant the database it was read from gets
	// replaced wholesale - flush rather than try to invalidate individual
	// keys one at a time (see cache.Client.FlushAll's own doc comment on
	// why a full flush is safe on this project's dedicated Redis instance).
	flushCtx, cancelFlush := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelFlush()
	if err := h.store.Cache.FlushAll(flushCtx); err != nil {
		return restoreResultDTO{}, fmt.Errorf("database restored from %s, but the cache could not be flushed - restart the panel process: %w", safety.Filename, err)
	}

	return restoreResultDTO{
		SafetyBackup: safety,
		Detail:       "Database restored. A safety backup of the previous data was taken first: " + safety.Filename,
	}, nil
}

type restoreRequestDTO struct {
	Confirm bool `json:"confirm"`
}

// handleRestoreBackup restores the database from one of the backups
// already sitting in BackupDir - no upload needed, the file's own
// filename (validated against the same allowlist regex download/delete
// use) is enough provenance.
func (h *Handler) handleRestoreBackup(c *gin.Context) {
	filename := c.Param("filename")
	if !backupFilenamePattern.MatchString(filename) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "Invalid backup filename"})
		return
	}
	var req restoreRequestDTO
	if err := c.ShouldBindJSON(&req); err != nil || !req.Confirm {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "This is a destructive operation - resend with {\"confirm\": true} to proceed"})
		return
	}
	path := filepath.Join(h.backupDir, filename)
	f, err := os.Open(path)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Backup not found"})
		return
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	result, err := h.restoreFromReader(ctx, f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// handleRestoreUpload restores (or, for a recognized legacy panel export,
// imports) the database from an uploaded file. What actually happens
// depends on detectUploadFormat's table/key-signature-based classification
// (see legacyimport.go): a real pg_dump upload goes through the same
// restoreFromReader path as handleRestoreBackup; a mysqldump matching the
// legacy Marzban/Rapido schema, or a real Hiddify Panel `hiddifypanel
// backup` JSON export, is parsed and mapped through loadLegacyImport
// instead - both interpreters produce the exact same ImportedData shape,
// so this handler doesn't need to know or care which one ran. Anything
// else is rejected with a clear "not supported yet" message rather than
// attempting a mis-restore - see the Phase 8.2 plan for the full list of
// source panels this is meant to grow to cover, one at a time.
func (h *Handler) handleRestoreUpload(c *gin.Context) {
	if c.PostForm("confirm") != "true" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "This is a destructive operation - resend with confirm=true to proceed"})
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "No file uploaded"})
		return
	}

	if err := os.MkdirAll(h.backupDir, 0o700); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not create a scratch directory for the upload"})
		return
	}
	tmpPath := filepath.Join(h.backupDir, backupUploadPrefix+newBackupFilename(time.Now()))
	if err := c.SaveUploadedFile(fileHeader, tmpPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not save the uploaded file"})
		return
	}
	defer os.Remove(tmpPath)

	detected, err := detectUploadFormat(tmpPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	switch detected.format {
	case uploadFormatNativePostgres:
		f, err := os.Open(tmpPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not reopen the uploaded file"})
			return
		}
		defer f.Close()
		result, err := h.restoreFromReader(ctx, f)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)

	case uploadFormatMarzbanMySQL:
		data := legacyimport.FromMarzbanMySQLDump(detected.mysqlDump)
		result, err := h.loadLegacyImport(ctx, data)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)

	case uploadFormatHiddifyJSON:
		data, err := legacyimport.FromHiddifyJSON(detected.jsonRaw)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
			return
		}
		result, err := h.loadLegacyImport(ctx, data)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)

	case uploadFormatPasarGuardPostgres:
		data, err := legacyimport.FromPasarGuard(detected.pgDump)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
			return
		}
		result, err := h.loadLegacyImport(ctx, data)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)

	default:
		c.JSON(http.StatusBadRequest, gin.H{"detail": "This doesn't look like a supported backup format - a Postgres pg_dump (from this panel, or from a PasarGuard panel on PostgreSQL/TimescaleDB), a Marzban mysqldump, or a Hiddify Panel export are supported today"})
	}
}

func (h *Handler) handleDeleteBackup(c *gin.Context) {
	filename := c.Param("filename")
	if !backupFilenamePattern.MatchString(filename) {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "Invalid backup filename"})
		return
	}
	path := filepath.Join(h.backupDir, filename)
	if _, err := os.Stat(path); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Backup not found"})
		return
	}
	if err := os.Remove(path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not delete backup"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"detail": "Backup removed successfully"})
}
