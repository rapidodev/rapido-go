package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const restorableName = "rapido_20260101T000000.000Z.sql.gz"

func gzipped(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func backupFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// A file that isn't a whole dump must be refused while everything is still as it
// was: no maintenance mode, no safety backup, and psql never hears of it.
func TestABackupThatIsNotWholeIsRefusedBeforeAnythingHappens(t *testing.T) {
	whole := gzipped(t, pgDumpHeader+"\nSELECT 1;\n"+pgDumpTrailer+"\n")
	cases := map[string][]byte{
		"cut off":                whole[:len(whole)-10],
		"pg_dump never finished": gzipped(t, pgDumpHeader+"\nSELECT 1;\n"),
		"not gzip at all":        []byte("nope"),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			router, token, handler := newTestRouterAndHandler(t)
			handler.backupDir = t.TempDir()
			handler.dumpDatabase = fakeDump("-- current\n")
			called := false
			handler.restoreDatabase = func(context.Context, io.Reader) error {
				called = true
				return nil
			}
			if err := os.WriteFile(filepath.Join(handler.backupDir, restorableName), content, 0o600); err != nil {
				t.Fatal(err)
			}
			resp := doRequest(t, router, "POST", "/api/settings/backup/"+restorableName+"/restore", token, map[string]interface{}{"confirm": true})
			if resp.Code == http.StatusOK {
				t.Fatalf("restored: %v", resp.Body)
			}
			if called {
				t.Error("psql was handed the file")
			}
			if files := backupFilesIn(t, handler.backupDir); len(files) != 1 {
				t.Errorf("a safety backup was taken for nothing: %v", files)
			}
		})
	}
}

func TestAFailedDumpLeavesNothingThatLooksLikeABackup(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	handler.backupDir = t.TempDir()
	handler.dumpDatabase = func(_ context.Context, w *os.File) error {
		w.WriteString("half a dump")
		return errors.New("pg_dump died")
	}
	if resp := doRequest(t, router, "POST", "/api/settings/backup", token, nil); resp.Code == http.StatusOK {
		t.Fatalf("a failed dump was reported as a backup: %v", resp.Body)
	}
	if files := backupFilesIn(t, handler.backupDir); len(files) != 0 {
		t.Errorf("left behind: %v", files)
	}
}

func TestAKilledDumpNeverShowsUpAsABackup(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	handler.backupDir = t.TempDir()
	handler.dumpDatabase = func(_ context.Context, w *os.File) error {
		// While a dump is being written, the list must not offer it.
		list := doRequest(t, router, "GET", "/api/settings/backup", token, nil)
		if strings.Contains(string(list.Raw), "rapido_") {
			t.Errorf("listed while still being written: %s", list.Raw)
		}
		_, err := w.WriteString("dump")
		return err
	}
	if resp := doRequest(t, router, "POST", "/api/settings/backup", token, nil); resp.Code != http.StatusOK {
		t.Fatalf("%d %v", resp.Code, resp.Body)
	}
	list := doRequest(t, router, "GET", "/api/settings/backup", token, nil)
	if !strings.Contains(string(list.Raw), "rapido_") {
		t.Errorf("not listed once done: %s", list.Raw)
	}
}

func TestStaleScratchFilesAreCleanedUp(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, backupScratchPrefix+"old")
	fresh := filepath.Join(dir, backupUploadPrefix+"fresh")
	keep := filepath.Join(dir, restorableName)
	for _, path := range []string{old, fresh, keep} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-7 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(keep, past, past); err != nil {
		t.Fatal(err)
	}
	removeStaleBackupScratch(dir)
	if files := backupFilesIn(t, dir); len(files) != 2 {
		t.Errorf("left %v, want the fresh upload and the real backup", files)
	}
}

func TestBackupsAreReadableOnlyByThePanel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix permissions here")
	}
	router, token, handler := newTestRouterAndHandler(t)
	handler.backupDir = filepath.Join(t.TempDir(), "backups")
	handler.dumpDatabase = fakeDump("secrets")
	resp := doRequest(t, router, "POST", "/api/settings/backup", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %v", resp.Code, resp.Body)
	}
	file, err := os.Stat(filepath.Join(handler.backupDir, resp.Body["filename"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	if file.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", file.Mode().Perm())
	}
	dir, err := os.Stat(handler.backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %o, want 700", dir.Mode().Perm())
	}
}

// The safety backup a restore takes must not push the backup being restored, or
// any other, out of the rotation.
func TestARestoreKeepsEveryExistingBackup(t *testing.T) {
	router, token, handler := newTestRouterAndHandler(t)
	handler.backupDir = t.TempDir()
	handler.backupKeep = 1
	handler.dumpDatabase = fakeDump("-- current\n")
	handler.restoreDatabase = func(context.Context, io.Reader) error { return nil }
	whole := gzipped(t, pgDumpHeader+"\n"+pgDumpTrailer+"\n")
	if err := os.WriteFile(filepath.Join(handler.backupDir, restorableName), whole, 0o600); err != nil {
		t.Fatal(err)
	}
	resp := doRequest(t, router, "POST", "/api/settings/backup/"+restorableName+"/restore", token, map[string]interface{}{"confirm": true})
	if resp.Code != http.StatusOK {
		t.Fatalf("%d %v", resp.Code, resp.Body)
	}
	files := backupFilesIn(t, handler.backupDir)
	if len(files) != 2 {
		t.Errorf("files after the restore = %v, want the source and the safety backup", files)
	}
}

// A backup carries the secret that signs tokens: whoever holds one can be anybody,
// so only the top tier may take one away or load one.
func TestWhatABackupHoldsIsForOwnersOnly(t *testing.T) {
	router, ownerToken, handler := newTestRouterAndHandler(t)
	handler.backupDir = t.TempDir()
	handler.dumpDatabase = fakeDump("x")
	sudo := createSudoAdmin(t, router, ownerToken, "plain-sudo", "pw123456")
	if err := os.WriteFile(filepath.Join(handler.backupDir, restorableName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, req := range []struct{ method, path string }{
		{"GET", "/api/settings/backup/" + restorableName},
		{"DELETE", "/api/settings/backup/" + restorableName},
		{"POST", "/api/settings/backup/" + restorableName + "/restore"},
		{"POST", "/api/settings/backup/restore-upload"},
	} {
		if resp := doRequest(t, router, req.method, req.path, sudo, map[string]interface{}{"confirm": true}); resp.Code != http.StatusForbidden {
			t.Errorf("%s %s as sudo: %d, want 403", req.method, req.path, resp.Code)
		}
	}
	// Seeing that backups exist and making one gives nothing away.
	if resp := doRequest(t, router, "GET", "/api/settings/backup", sudo, nil); resp.Code != http.StatusOK {
		t.Errorf("list as sudo: %d", resp.Code)
	}
	if resp := doRequest(t, router, "POST", "/api/settings/backup", sudo, nil); resp.Code != http.StatusOK {
		t.Errorf("create as sudo: %d", resp.Code)
	}
	if resp := doRequest(t, router, "GET", "/api/settings/backup/"+restorableName, ownerToken, nil); resp.Code != http.StatusOK {
		t.Errorf("download as owner: %d", resp.Code)
	}
}

func TestCopyDumpLeavesOutOnlyPgDumpsOwnRestrictLines(t *testing.T) {
	long := strings.Repeat("x", 3<<20)
	input := "\\restrict abc\n" +
		"-- comment\n" +
		"COPY public.t (a) FROM stdin;\n" +
		"\\restrict is somebody's data\n" +
		long + "\n" +
		"\\.\n" +
		"SELECT 1;\n" +
		"\\unrestrict abc\n" +
		"last line without a newline"
	want := "-- comment\n" +
		"COPY public.t (a) FROM stdin;\n" +
		"\\restrict is somebody's data\n" +
		long + "\n" +
		"\\.\n" +
		"SELECT 1;\n" +
		"last line without a newline"
	var out bytes.Buffer
	if err := copyDumpForRestrictedPsql(&out, strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	if out.String() != want {
		t.Errorf("output differs (got %d bytes, want %d)", out.Len(), len(want))
	}
}

func TestTailWriterKeepsTheEnd(t *testing.T) {
	tail := &tailWriter{limit: 4}
	for _, chunk := range []string{"ab", "cdef", "g", "hijklmnop", "q"} {
		if n, err := tail.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	if string(tail.buf) != "mnopq"[1:] {
		t.Errorf("tail = %q", tail.buf)
	}
}

func throwawayDatabase(t *testing.T) string {
	t.Helper()
	for _, tool := range []string{"pg_dump", "psql"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("rapido_backup_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()", name)
		admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize())
		admin.Close(ctx)
	})
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + name
	return parsed.String()
}

func execSQL(t *testing.T, databaseURL, statement string) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), statement); err != nil {
		t.Fatal(err)
	}
}

func canaryNote(t *testing.T, databaseURL string) string {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var note string
	if err := conn.QueryRow(context.Background(), "SELECT note FROM canary").Scan(&note); err != nil {
		t.Fatalf("canary: %v", err)
	}
	return note
}

// psql runs whatever backslash commands it reads, and a dump can be an uploaded
// file: without \restrict, one line in it is a shell in the panel's container.
func TestARestoreDoesNotRunShellCommandsFromTheDump(t *testing.T) {
	databaseURL := throwawayDatabase(t)
	execSQL(t, databaseURL, "CREATE TABLE canary (note text); INSERT INTO canary VALUES ('untouched')")
	marker := filepath.Join(t.TempDir(), "pwned")
	evil := "-- PostgreSQL database dump\nCREATE TABLE canary (note text);\n\\! touch " + marker + "\nINSERT INTO canary VALUES ('replaced');\n"

	err := execPsqlRestore(context.Background(), databaseURL, bytes.NewReader(gzipped(t, evil)))
	if err == nil {
		t.Error("a dump with a shell command restored without complaint")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the dump ran a shell command")
	}
	if got := canaryNote(t, databaseURL); got != "untouched" {
		t.Errorf("canary = %q: the failed restore changed the database", got)
	}
}

// The file pg_dump itself writes carries \restrict and \unrestrict lines, and
// must still restore under ours.
func TestARealDumpRestoresUnderRestriction(t *testing.T) {
	databaseURL := throwawayDatabase(t)
	execSQL(t, databaseURL, `CREATE TABLE canary (note text); INSERT INTO canary VALUES (E'\\restrict not a command')`)

	path := filepath.Join(t.TempDir(), "dump.sql.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := execPgDump(context.Background(), databaseURL, f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	execSQL(t, databaseURL, "UPDATE canary SET note = 'changed'")

	dump, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dump.Close()
	if err := verifyCompleteDump(dump); err != nil {
		t.Fatalf("a real dump failed verification: %v", err)
	}
	if err := execPsqlRestore(context.Background(), databaseURL, dump); err != nil {
		t.Fatal(err)
	}
	if got := canaryNote(t, databaseURL); got != `\restrict not a command` {
		t.Errorf("canary = %q", got)
	}
}

type brokenReader struct {
	data []byte
	err  error
}

func (b *brokenReader) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, b.err
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

// On a clean end of input psql commits what it has, and DROP SCHEMA came first:
// a stream that breaks must never get as far as that.
func TestARestoreStreamThatBreaksChangesNothing(t *testing.T) {
	databaseURL := throwawayDatabase(t)
	execSQL(t, databaseURL, "CREATE TABLE canary (note text); INSERT INTO canary VALUES ('untouched')")
	// Incompressible, so the cut falls inside what psql has already been reading.
	var body strings.Builder
	body.WriteString("CREATE TABLE canary (note text);\nINSERT INTO canary VALUES ('replaced');\n")
	for i := 0; i < 200000; i++ {
		fmt.Fprintf(&body, "-- %d %x\n", i, i*7919)
	}
	whole := gzipped(t, body.String())

	err := execPsqlRestore(context.Background(), databaseURL, &brokenReader{data: whole[:len(whole)/2], err: errors.New("disk read failed")})
	if err == nil {
		t.Fatal("no error from a stream that broke")
	}
	if got := canaryNote(t, databaseURL); got != "untouched" {
		t.Errorf("canary = %q: a broken stream was committed", got)
	}
}
