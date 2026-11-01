package blob

import (
	"errors"
	"fmt"
	"path/filepath"

	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
)

// validateRegisteredReferences runs under the publication/GC lock. A registered
// Bot absent from the filesystem is damaged state, not evidence of zero grants.
func (s *Store) validateRegisteredReferences() error {
	data, err := store.ReadFile(filepath.Join(s.root, "bots.json"))
	if err != nil {
		return fmt.Errorf("read Bot catalog before collection: %w", err)
	}
	var catalog struct {
		SchemaVersion int `json:"schemaVersion"`
		Bots          []struct {
			BotID        string `json:"botId"`
			DesiredState string `json:"desiredState"`
			Archived     bool   `json:"archived"`
		} `json:"bots"`
	}
	if err := protocol.DecodeStrictLimit(data, &catalog, 64<<20); err != nil {
		return fmt.Errorf("parse Bot catalog before collection: %w", err)
	}
	if catalog.SchemaVersion != 2 || catalog.Bots == nil {
		return errors.New("invalid Bot catalog before collection")
	}
	seen := make(map[string]bool, len(catalog.Bots))
	for _, bot := range catalog.Bots {
		if !validID(bot.BotID) || seen[bot.BotID] || (bot.DesiredState != "running" && bot.DesiredState != "stopped") || (bot.Archived && bot.DesiredState != "stopped") {
			return errors.New("invalid registered Bot before collection")
		}
		seen[bot.BotID] = true
		if _, err := s.refDir(bot.BotID, false); err != nil {
			return fmt.Errorf("registered Bot %s has missing or unsafe reference state: %w", bot.BotID, err)
		}
	}
	return nil
}
