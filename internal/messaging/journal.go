package messaging

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"clawsynapse/internal/protocol"
)

// StoredMessage adds a local replay cursor without changing the signed envelope.
// Cursors belong to this node's durable received-message journal, not NATS.
type StoredMessage struct {
	protocol.MessageEnvelope
	Cursor uint64 `json:"cursor"`
}

type MessagesPage struct {
	Items  []StoredMessage `json:"items"`
	Cursor uint64          `json:"cursor"`
}

type RequestState struct {
	Message     protocol.MessageEnvelope   `json:"message"`
	Publication string                     `json:"publication"`
	Status      string                     `json:"status"`
	Events      []protocol.MessageEnvelope `json:"events"`
}

type publication struct {
	Message     protocol.MessageEnvelope `json:"message"`
	Key         string                   `json:"key,omitempty"`
	Fingerprint string                   `json:"fingerprint"`
	Published   bool                     `json:"published"`
	Previous    string                   `json:"previous,omitempty"`
}

type admittedWork struct {
	Key     string                   `json:"key"`
	Message protocol.MessageEnvelope `json:"message,omitzero"`
	State   string                   `json:"state"`
	RunID   string                   `json:"runId"`
	Seq     uint64                   `json:"seq"`
}

type journalRecord struct {
	Publication *publication   `json:"publication,omitempty"`
	Published   string         `json:"published,omitempty"`
	Received    *StoredMessage `json:"received,omitempty"`
	Work        *admittedWork  `json:"work,omitempty"`
}

type messageJournal struct {
	file         *os.File
	publications map[string]*publication
	keys         map[string]string
	inbox        []StoredMessage
	received     map[string]bool
	work         map[string]*admittedWork
	pending      []string
	workQueues   [workerCount][]string
	workHeads    [workerCount]int
	tails        map[string]string
}

func openMessageJournal(path string) (*messageJournal, error) {
	if path == "" {
		return nil, errors.New("messaging state path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	fail := func(err error) (*messageJournal, error) { _ = f.Close(); return nil, err }
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(fmt.Errorf("lock messaging journal: %w", err))
	}
	j := &messageJournal{file: f, publications: make(map[string]*publication), keys: make(map[string]string), received: make(map[string]bool), work: make(map[string]*admittedWork), tails: make(map[string]string)}
	r := bufio.NewReader(f)
	var offset int64
	for {
		line, err := r.ReadBytes('\n')
		if err == io.EOF {
			// A crash may leave only the final uncommitted append incomplete.
			if len(line) != 0 {
				if err := f.Truncate(offset); err != nil {
					return fail(err)
				}
				if err := f.Sync(); err != nil {
					return fail(err)
				}
			}
			break
		}
		if err != nil {
			return fail(err)
		}
		var record journalRecord
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&record); err != nil {
			return fail(fmt.Errorf("read messaging journal at %d: %w", offset, err))
		}
		j.apply(record)
		offset += int64(len(line))
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fail(err)
	}
	err = dir.Sync()
	_ = dir.Close()
	if err != nil {
		return fail(err)
	}
	return j, nil
}

func (j *messageJournal) append(record journalRecord) error {
	if j.file == nil {
		return os.ErrClosed
	}
	wire := record
	if record.Work != nil {
		work := *record.Work
		work.Message = protocol.MessageEnvelope{}
		wire.Work = &work
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	offset, err := j.file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err = j.file.Write(data); err == nil {
		err = j.file.Sync()
	}
	if err != nil {
		// Never let a failed append turn into a committed prefix on the next call.
		_ = j.file.Truncate(offset)
		_ = j.file.Sync()
		return err
	}
	j.apply(record)
	return nil
}

func (j *messageJournal) apply(record journalRecord) {
	if p := record.Publication; p != nil {
		if j.publications[p.Message.ID] == nil {
			j.pending = append(j.pending, p.Message.ID)
		}
		j.publications[p.Message.ID] = p
		if p.Key != "" {
			j.keys[p.Key] = p.Message.ID
		}
		if p.Message.RunID != "" {
			j.tails[eventKey(p.Message)] = p.Message.ID
		}
	}
	if record.Published != "" {
		if p := j.publications[record.Published]; p != nil {
			p.Published = true
		}
	}
	if m := record.Received; m != nil {
		j.inbox = append(j.inbox, *m)
		j.received[messageKey(m.MessageEnvelope)] = true
	}
	if w := record.Work; w != nil {
		if w.Message.ID == "" {
			if previous := j.work[w.Key]; previous != nil {
				w.Message = previous.Message
			} else if record.Received != nil {
				w.Message = record.Received.MessageEnvelope
			}
		}
		if j.work[w.Key] == nil {
			shard := workShard(w.Message)
			j.workQueues[shard] = append(j.workQueues[shard], w.Key)
		}
		j.work[w.Key] = w
	}
}

func eventKey(env protocol.MessageEnvelope) string {
	return env.To + "/" + env.RequestID + "/" + env.RunID
}

func messageKey(env protocol.MessageEnvelope) string { return env.From + "/" + env.ID }

func cloneJSON[T any](value T) T {
	data, _ := json.Marshal(value)
	var out T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	_ = decoder.Decode(&out)
	return out
}
