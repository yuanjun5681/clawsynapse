package transfer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"clawsynapse/internal/blob"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"

	"github.com/nats-io/nats.go"
)

type ReceivedNotifier func(rec TransferRecord)

type Service struct {
	mu           sync.Mutex
	opMu         sync.Mutex
	log          *slog.Logger
	peers        *discovery.Registry
	bus          *natsbus.Client
	messaging    *messaging.Service
	botID        string
	identity     *identity.Identity
	trustMode    string
	transferDir  string
	workspace    string
	blobs        *blob.Store
	storageBotID string
	quotaBytes   int64
	maxFileSize  int64
	ttl          time.Duration
	transfers    map[string]*TransferRecord
	onReceived   ReceivedNotifier
	configured   bool
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	closing      bool
}

func NewService(log *slog.Logger, peers *discovery.Registry, bus *natsbus.Client, msgSvc *messaging.Service, botID string, id *identity.Identity, trustMode string, cfg TransferConfig) *Service {
	maxFileSize := cfg.MaxFileSize
	if maxFileSize <= 0 {
		maxFileSize = 104857600
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{log: log, peers: peers, bus: bus, messaging: msgSvc, botID: botID, identity: id, trustMode: trustMode, transferDir: cfg.TransferDir, workspace: cfg.Workspace, blobs: cfg.Blobs, storageBotID: cfg.BotID, quotaBytes: cfg.QuotaBytes, maxFileSize: maxFileSize, ttl: parseTTL(cfg.TTL), transfers: make(map[string]*TransferRecord), ctx: ctx, cancel: cancel}
}

// ConfigurePersistence loads the authoritative durable grants before the Runtime
// announces success. Missing/corrupt content is an error, never an empty history.
func (s *Service) ConfigurePersistence() (err error) {
	defer func() { err = safeFileError(err) }()
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.configure()
}
func (s *Service) configure() error {
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing {
		return errors.New("transfer service closed")
	}
	if s.configured {
		return nil
	}
	if s.blobs == nil || s.storageBotID == "" || s.workspace == "" || s.transferDir == "" {
		return errors.New("Bot blob store, transfer directory, and workspace are required")
	}
	if s.botID != s.storageBotID {
		return errors.New("transfer identity does not match Bot scope")
	}
	if err := s.blobs.ValidateLayout(s.storageBotID, s.transferDir, s.workspace); err != nil {
		return err
	}
	refs, err := s.blobs.List(s.storageBotID)
	if err != nil {
		return err
	}
	loaded := make(map[string]*TransferRecord, len(refs))
	for _, ref := range refs {
		rec, err := recordFromReference(ref)
		if err != nil {
			return err
		}
		if rec.Direction == "outbound" && rec.Status == "pending" {
			rec.Status = "uncertain"
			rec.Error = "daemon stopped before remote publication was confirmed"
			data, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			if err := s.blobs.UpdateData(s.storageBotID, rec.TransferID, data); err != nil {
				return err
			}
		}
		f, err := s.blobs.Open(s.storageBotID, ref.TransferID)
		if err != nil {
			return fmt.Errorf("recover transfer %s: %w", ref.TransferID, err)
		}
		if err := f.Close(); err != nil {
			return err
		}
		loaded[ref.TransferID] = rec
	}
	s.mu.Lock()
	s.transfers = loaded
	s.mu.Unlock()
	s.configured = true
	return nil
}
func recordFromReference(ref blob.Reference) (*TransferRecord, error) {
	var rec TransferRecord
	if err := protocol.DecodeStrict(ref.Data, &rec); err != nil {
		return nil, fmt.Errorf("corrupt transfer %s: %w", ref.TransferID, err)
	}
	if rec.TransferID != ref.TransferID {
		return nil, errors.New("invalid persisted transfer record")
	}
	switch rec.Direction {
	case "inbound", "outbound", "imported":
	default:
		return nil, errors.New("invalid persisted transfer direction")
	}
	switch rec.Status {
	case "pending", "completed", "failed", "uncertain":
	default:
		return nil, errors.New("invalid persisted transfer status")
	}
	if rec.Direction == "inbound" && rec.NotificationState != "pending" && rec.NotificationState != "admitted" {
		return nil, errors.New("invalid persisted notification admission")
	}
	rec.BlobDigest = ref.Digest
	rec.FileSize = ref.Size
	if rec.Checksum == "" {
		digest, _ := hex.DecodeString(ref.Digest)
		rec.Checksum = "SHA-256=" + base64.URLEncoding.EncodeToString(digest)
	}
	// Workspace paths are derived only when execution actually needs a copy.
	rec.LocalPath = ""
	return &rec, nil
}
func (s *Service) OnReceived(fn ReceivedNotifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onReceived = fn
}
func (s *Service) Enabled() bool { return s.bus != nil && s.bus.HasJetStream() }
func (s *Service) Start(ctx context.Context) error {
	if err := s.ConfigurePersistence(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return errors.New("transfer service closed")
	}
	s.mu.Unlock()
	if !s.Enabled() {
		s.log.Warn("transfer service disabled: jetstream not available")
		return s.notifyPending()
	}
	if _, err := s.ensureBucket(s.botID); err != nil {
		if isJetStreamUnavailable(err) {
			s.log.Warn("transfer service disabled: jetstream unavailable")
			return s.notifyPending()
		}
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		cancel()
		return errors.New("transfer service closed")
	}
	s.cancel()
	s.ctx = ctx
	s.cancel = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go func() { defer s.wg.Done(); s.watchBucket(ctx) }()
	return s.notifyPending()
}
func (s *Service) Close() error {
	s.mu.Lock()
	s.closing = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.opMu.Lock()
	s.opMu.Unlock()
	s.wg.Wait()
	return nil
}
func (s *Service) watchBucket(ctx context.Context) {
	store, err := s.getBucket(s.botID)
	if err != nil {
		s.log.Error("watch bucket failed", slog.Any("error", err))
		return
	}
	watcher, err := store.Watch(nats.IgnoreDeletes())
	if err != nil {
		s.log.Error("start watch failed", slog.Any("error", err))
		return
	}
	defer watcher.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case info, ok := <-watcher.Updates():
			if !ok {
				return
			}
			if info == nil {
				continue
			}
			if err := s.pullAndSave(info.Name, bucketName(s.botID), ""); err != nil {
				s.log.Warn("watch pull failed", slog.String("transferId", info.Name), slog.Any("error", err))
			}
		}
	}
}
func (s *Service) trustedPeer(id string) error {
	if id == "" {
		return errors.New("missing transfer sender")
	}
	if s.trustMode == "open" {
		return nil
	}
	if s.peers == nil {
		return errors.New("peer registry unavailable")
	}
	peer, ok := s.peers.Get(id)
	if !ok || peer.TrustStatus != types.TrustTrusted {
		return protocol.NewError("control.unauthorized", "peer is not trusted")
	}
	if peer.AuthStatus != types.AuthAuthenticated {
		return errors.New("peer is not authenticated")
	}
	return nil
}

// SendFile deliberately accepts only this Bot's workspace. Arbitrary host files
// require the administrator-only ImportFile operation before authorized forwarding.
func (s *Service) SendFile(req SendFileRequest) (result SendFileResult, err error) {
	defer func() { err = safeFileError(err) }()
	if err := s.ConfigurePersistence(); err != nil {
		return SendFileResult{}, err
	}
	f, err := openWorkspaceFile(s.workspace, req.FilePath)
	if err != nil {
		return SendFileResult{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return SendFileResult{}, err
	}
	return s.send(req, f, info.Size(), filepath.Base(req.FilePath))
}
func (s *Service) ForwardFile(id string, req SendFileRequest) (result SendFileResult, err error) {
	defer func() { err = safeFileError(err) }()
	f, err := s.OpenFile(id)
	if err != nil {
		return SendFileResult{}, err
	}
	defer f.Close()
	s.mu.Lock()
	rec, ok := s.transfers[id]
	if !ok {
		s.mu.Unlock()
		return SendFileResult{}, blob.ErrNotFound
	}
	size, name, mimeType := rec.FileSize, rec.FileName, rec.MimeType
	s.mu.Unlock()
	if req.MimeType == "" {
		req.MimeType = mimeType
	}
	return s.send(req, f, size, name)
}
func (s *Service) send(req SendFileRequest, r io.Reader, size int64, name string) (SendFileResult, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.configure(); err != nil {
		return SendFileResult{}, err
	}
	if !s.Enabled() {
		return SendFileResult{}, protocol.NewError(protocol.ErrTransferDisabled, "transfer disabled: jetstream not available")
	}
	if s.peers == nil {
		return SendFileResult{}, errors.New("peer registry unavailable")
	}
	if _, ok := s.peers.Get(req.TargetBot); !ok {
		return SendFileResult{}, errors.New("target peer not found")
	}
	if err := s.trustedPeer(req.TargetBot); err != nil {
		return SendFileResult{}, err
	}
	if size > s.maxFileSize {
		return SendFileResult{}, protocol.NewError(protocol.ErrTransferFileTooLarge, "file exceeds transfer size limit")
	}
	remote, err := s.ensureBucket(req.TargetBot)
	if err != nil {
		return SendFileResult{}, protocol.NewError(protocol.ErrTransferBucketError, err.Error())
	}
	id := newTransferID()
	now := time.Now().UnixMilli()
	rec := &TransferRecord{TransferID: id, Direction: "outbound", PeerBot: req.TargetBot, FileName: safeName(name), MimeType: req.MimeType, Status: "pending", Bucket: bucketName(req.TargetBot), Metadata: req.Metadata, CreatedAt: now}
	if err := s.commit(rec, r, blob.WriteOptions{MaxBytes: s.maxFileSize, ExpectedSize: size, QuotaBytes: s.quotaBytes}); err != nil {
		return SendFileResult{}, err
	}
	f, err := s.blobs.Open(s.storageBotID, id)
	if err != nil {
		return SendFileResult{}, err
	}
	defer f.Close()
	meta := &nats.ObjectMeta{Name: id, Headers: nats.Header{"X-From": {s.botID}, "X-File-Name": {rec.FileName}}}
	if req.MimeType != "" {
		meta.Headers.Set("X-Mime-Type", req.MimeType)
	}
	if len(req.Metadata) > 0 {
		data, err := json.Marshal(req.Metadata)
		if err != nil {
			return SendFileResult{}, err
		}
		meta.Headers.Set("X-Metadata", string(data))
	}
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	objInfo, err := remote.Put(meta, safeFileReader{f}, nats.Context(ctx))
	if err != nil {
		rec.Status = "failed"
		rec.Error = err.Error()
		if persistErr := s.persist(rec); persistErr != nil {
			return SendFileResult{}, errors.Join(err, persistErr)
		}
		return SendFileResult{}, protocol.NewError(protocol.ErrTransferFailed, err.Error())
	}
	rec.Status = "completed"
	rec.Checksum = objInfo.Digest
	rec.CompletedAt = time.Now().UnixMilli()
	if err := s.persist(rec); err != nil {
		return SendFileResult{}, err
	}
	payload, _ := json.Marshal(map[string]any{"transferId": id, "fileName": rec.FileName, "size": rec.FileSize, "mimeType": req.MimeType, "bucket": rec.Bucket})
	if s.messaging != nil {
		_, _ = s.messaging.Publish(messaging.PublishRequest{TargetBot: req.TargetBot, Type: "transfer.available", Payload: protocol.MessagePayload{Input: payload}, Metadata: req.Metadata})
	}
	return SendFileResult{TransferID: id, Bucket: rec.Bucket, Size: rec.FileSize, Checksum: rec.Checksum}, nil
}
func (s *Service) HandleTransferNotification(env protocol.MessageEnvelope) error {
	if !s.Enabled() {
		return errors.New("transfer service unavailable")
	}
	var payload struct {
		TransferID string `json:"transferId"`
		Bucket     string `json:"bucket"`
		FileName   string `json:"fileName"`
		Size       int64  `json:"size"`
		MimeType   string `json:"mimeType"`
	}
	if err := protocol.DecodeStrict(env.Payload.Input, &payload); err != nil {
		return err
	}
	if payload.Bucket != bucketName(s.botID) {
		return errors.New("transfer notification targets another Bot bucket")
	}
	if err := s.trustedPeer(env.From); err != nil {
		return err
	}
	return s.pullAndSave(payload.TransferID, payload.Bucket, env.From)
}
func (s *Service) pullAndSave(id, bucket, expectedFrom string) (err error) {
	defer func() { err = safeFileError(err) }()
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.configure(); err != nil {
		return err
	}
	if s.isAlreadyDownloaded(id) {
		return s.notify(id)
	}
	if bucket != bucketName(s.botID) {
		return errors.New("unauthorized transfer bucket")
	}
	if !s.Enabled() {
		return errors.New("jetstream unavailable")
	}
	remote, err := s.bus.JetStream().ObjectStore(bucket)
	if err != nil {
		return err
	}
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	info, err := remote.GetInfo(id, nats.Context(ctx))
	if err != nil {
		return err
	}
	if info.Opts != nil && info.Opts.Link != nil {
		return errors.New("linked transfer objects are not permitted")
	}
	if info.Size > uint64(s.maxFileSize) {
		return protocol.NewError(protocol.ErrTransferFileTooLarge, "file exceeds transfer size limit")
	}
	from := info.Headers.Get("X-From")
	if expectedFrom != "" && from != expectedFrom {
		return errors.New("transfer sender mismatch")
	}
	if err := s.trustedPeer(from); err != nil {
		return err
	}
	var metadata map[string]any
	if raw := info.Headers.Get("X-Metadata"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
			return err
		}
	}
	result, err := remote.Get(id, nats.Context(ctx))
	if err != nil {
		return err
	}
	defer result.Close()
	rec := &TransferRecord{TransferID: id, Direction: "inbound", PeerBot: from, FileName: safeName(info.Headers.Get("X-File-Name")), MimeType: info.Headers.Get("X-Mime-Type"), Checksum: info.Digest, Status: "completed", Bucket: bucket, Metadata: metadata, CreatedAt: info.ModTime.UnixMilli(), CompletedAt: time.Now().UnixMilli(), NotificationState: "pending"}
	if err := s.commit(rec, result, blob.WriteOptions{MaxBytes: s.maxFileSize, ExpectedSize: int64(info.Size), ExpectedDigest: info.Digest, QuotaBytes: s.quotaBytes}); err != nil {
		return err
	}
	return s.notify(id)
}
func (s *Service) commit(rec *TransferRecord, r io.Reader, opts blob.WriteOptions) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	opts.Data = data
	ref, err := s.blobs.Put(s.storageBotID, rec.TransferID, r, opts)
	if err != nil {
		return err
	}
	restored, err := recordFromReference(ref)
	if err != nil {
		return err
	}
	*rec = *restored
	snapshot := *rec
	snapshot.Metadata = cloneMetadata(rec.Metadata)
	s.mu.Lock()
	s.transfers[rec.TransferID] = &snapshot
	s.mu.Unlock()
	return nil
}
func (s *Service) persist(rec *TransferRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := s.blobs.UpdateData(s.storageBotID, rec.TransferID, data); err != nil {
		return err
	}
	snapshot := *rec
	snapshot.Metadata = cloneMetadata(rec.Metadata)
	s.mu.Lock()
	s.transfers[rec.TransferID] = &snapshot
	s.mu.Unlock()
	return nil
}
func (s *Service) notifyPending() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	ids := make([]string, 0, len(s.transfers))
	for id, rec := range s.transfers {
		if rec.Direction == "inbound" && rec.NotificationState == "pending" {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		if err := s.notify(id); err != nil {
			return err
		}
	}
	return nil
}

// Admission is persisted before the external callback. A crash after admission
// has an explicitly uncertain notification, never automatic duplicate execution.
func (s *Service) notify(id string) (err error) {
	defer func() { err = safeFileError(err) }()
	s.mu.Lock()
	rec, ok := s.transfers[id]
	fn := s.onReceived
	closing := s.closing
	if !ok || rec.Direction != "inbound" || rec.NotificationState != "pending" || fn == nil || closing {
		s.mu.Unlock()
		return nil
	}
	copy := *rec
	s.mu.Unlock()
	path, err := s.blobs.Workspace(s.storageBotID, id, s.workspace)
	if err != nil {
		return err
	}
	copy.LocalPath = ""
	copy.NotificationState = "admitted"
	if err := s.persist(&copy); err != nil {
		return err
	}
	copy.LocalPath = path
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil
	}
	s.wg.Add(1)
	s.mu.Unlock()
	copy.Metadata = cloneMetadata(copy.Metadata)
	go func() { defer s.wg.Done(); fn(copy) }()
	return nil
}
func (s *Service) ImportFile(path string) (result TransferInfo, err error) {
	defer func() { err = safeFileError(err) }()
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.configure(); err != nil {
		return TransferInfo{}, err
	}
	f, err := store.OpenRegular(path)
	if err != nil {
		return TransferInfo{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return TransferInfo{}, err
	}
	now := time.Now().UnixMilli()
	rec := &TransferRecord{TransferID: newTransferID(), Direction: "imported", FileName: safeName(info.Name()), Status: "completed", CreatedAt: now, CompletedAt: now, Metadata: map[string]any{"source": "administrator-import"}}
	if err := s.commit(rec, f, blob.WriteOptions{MaxBytes: s.maxFileSize, ExpectedSize: info.Size(), QuotaBytes: s.quotaBytes}); err != nil {
		return TransferInfo{}, err
	}
	return toTransferInfo(rec), nil
}
func (s *Service) OpenFile(id string) (reader io.ReadCloser, err error) {
	defer func() { err = safeFileError(err) }()
	if err := s.ConfigurePersistence(); err != nil {
		return nil, err
	}
	reader, err = s.blobs.Open(s.storageBotID, id)
	if err != nil {
		return nil, err
	}
	return safeFileReader{reader}, nil
}
func (s *Service) isAlreadyDownloaded(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.transfers[id]
	return ok && rec.Direction == "inbound" && rec.Status == "completed"
}
func (s *Service) ListTransfers() []TransferInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TransferInfo, 0, len(s.transfers))
	for _, rec := range s.transfers {
		out = append(out, toTransferInfo(rec))
	}
	return out
}
func (s *Service) GetTransfer(id string) (TransferInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.transfers[id]
	if !ok {
		return TransferInfo{}, false
	}
	return toTransferInfo(rec), true
}
func (s *Service) DeleteTransfer(id string) (err error) {
	defer func() { err = safeFileError(err) }()
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.configure(); err != nil {
		return err
	}
	if err := s.blobs.Delete(s.storageBotID, id); err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			return protocol.NewError(protocol.ErrTransferNotFound, "transfer not found")
		}
		return err
	}
	s.mu.Lock()
	delete(s.transfers, id)
	s.mu.Unlock()
	return nil
}
func toTransferInfo(rec *TransferRecord) TransferInfo {
	return TransferInfo{TransferID: rec.TransferID, Direction: rec.Direction, PeerBot: rec.PeerBot, FileName: rec.FileName, FileSize: rec.FileSize, MimeType: rec.MimeType, Checksum: rec.Checksum, Status: rec.Status, Metadata: cloneMetadata(rec.Metadata), CreatedAt: rec.CreatedAt, CompletedAt: rec.CompletedAt}
}
func cloneMetadata(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}
func safeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		return "file"
	}
	return name
}
func openWorkspaceFile(workspace, path string) (*os.File, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("file must be inside this Bot workspace; administrator import is required for external files")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe workspace path")
		}
		current = filepath.Join(current, part)
	}
	return store.OpenRegular(path)
}
func newTransferID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("tf-%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func isJetStreamUnavailable(err error) bool {
	return errors.Is(err, nats.ErrJetStreamNotEnabled) || errors.Is(err, nats.ErrNeeds262)
}

// Read/Close errors also cross the authorized file API boundary; os.File errors
// otherwise include the private immutable object's pathname.
type safeFileReader struct{ io.ReadCloser }

func (r safeFileReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	return n, safeFileError(err)
}
func (r safeFileReader) Close() error { return safeFileError(r.ReadCloser.Close()) }

func safeFileError(err error) error {
	if err == nil {
		return nil
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return fmt.Errorf("file access failed: %w", pathError.Err)
	}
	var linkError *os.LinkError
	if errors.As(err, &linkError) {
		return fmt.Errorf("file publication failed: %w", linkError.Err)
	}
	if strings.Contains(err.Error(), "symlink path is not allowed:") || strings.Contains(err.Error(), "not a regular file:") {
		return errors.New("unsafe file storage path")
	}
	return err
}
