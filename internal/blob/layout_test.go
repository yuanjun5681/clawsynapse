package blob

import "testing"

// Production Bot creation initializes this directory before registering the Bot;
// publication and recovery must never create a lost authoritative directory.
func prepareTestBot(t *testing.T, s *Store, bot string) {
	t.Helper()
	if _, err := s.refDir(bot, true); err != nil {
		t.Fatal(err)
	}
}
