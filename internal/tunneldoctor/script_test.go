package tunneldoctor

import (
	"os"
	"testing"
)

// Developer tool, skipped unless DOCTOR_DUMP_DIR is set: writes the two
// measurement scripts so they can be run on real servers by hand.
func TestDumpScripts(t *testing.T) {
	dir := os.Getenv("DOCTOR_DUMP_DIR")
	if dir == "" {
		t.Skip("DOCTOR_DUMP_DIR not set")
	}
	node := buildScript(scriptParams{Peer6: os.Getenv("DOCTOR_PEER6"), Peer4: os.Getenv("DOCTOR_PEER4"), ControlPort: 0, HASocket: ""})
	if err := os.WriteFile(dir+"/node.sh", []byte(node), 0o644); err != nil {
		t.Fatal(err)
	}
}
