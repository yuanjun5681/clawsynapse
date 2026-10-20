package dashboard

import (
	"context"
	"regexp"
	"time"

	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

const (
	dashboardRefreshInterval  = 3 * time.Second
	dashboardTabCount         = 5
	dashboardMinWidth         = 60
	dashboardMinHeight        = 20
	dashboardNarrowBreakpoint = 100
	dashboardPageStep         = 6
	dashboardDefaultLogLines  = 80
)

const (
	boxTL = "╭"
	boxTR = "╮"
	boxBL = "╰"
	boxBR = "╯"
	boxH  = "─"
	boxV  = "│"
	boxHH = "━"
)

const (
	dotFull  = "●"
	dotHalf  = "◐"
	dotEmpty = "○"
	arrowUp  = "▲"
	arrowDn  = "▼"
	arrowR   = "▸"
	diamond  = "◈"
)

const (
	dashboardTagAccent   = "accent"
	dashboardTagMuted    = "muted"
	dashboardTagSection  = "section"
	dashboardTagGood     = "good"
	dashboardTagWarn     = "warn"
	dashboardTagBad      = "bad"
	dashboardTagSelected = "selected"
	dashboardTagDim      = "dim"
	dashboardTagLogInfo  = "log_info"
	dashboardTagLogWarn  = "log_warn"
	dashboardTagLogError = "log_error"
	dashboardTagLogDebug = "log_debug"
)

var dashboardANSIRegexp = regexp.MustCompile(`\x1b\[[0-9;]*m`)

type Client interface {
	Get(ctx context.Context, endpoint string) (types.APIResult, error)
	Put(ctx context.Context, endpoint string, payload any) (types.APIResult, error)
}

type LogProvider interface {
	ReadLogs(ctx context.Context, lines int) (string, error)
}

type Options struct {
	APIAddr   string
	Timeout   time.Duration
	Version   string
	LogSource string
	LogLines  int
	Client    Client
	Logs      LogProvider
}

type config struct {
	APIAddr string
	Timeout time.Duration
}

type snapshot struct {
	Health     health
	Peers      []types.Peer
	Messages   []protocol.MessageEnvelope
	Logs       string
	ConfigData map[string]any
	Updated    time.Time
}

type health struct {
	PeersCount int
	NATS       natsState
}

type natsState struct {
	Name             string
	ServerURL        string
	Connected        bool
	Status           string
	Disconnects      int64
	Reconnects       int64
	InMsgs           int64
	OutMsgs          int64
	InBytes          int64
	OutBytes         int64
	LastError        string
	ConnectedAt      int64
	LastDisconnectAt int64
	LastReconnectAt  int64
}

type configFieldKind int

const (
	cfkText configFieldKind = iota
	cfkEnum
	cfkBool
	cfkStringSlice
)

type configField struct {
	Label   string
	Key     string
	Group   string
	Kind    configFieldKind
	Enums   []string
	Value   string
	EditBuf string
	Changed bool
}

type configEditState struct {
	fields     []configField
	editing    bool
	editIdx    int
	dirty      bool
	statusMsg  string
	statusAt   time.Time
	configPath string
}

type configSaveMsg struct {
	err error
}

type refreshMsg struct {
	snapshot snapshot
	err      error
}

type layout struct {
	width  int
	height int
	narrow bool

	bodyHeight int

	splitLeftW  int
	splitRightW int

	logsLeftW  int
	logsRightW int

	cardWidth int
}

type panelTheme struct {
	border func(string) string
	title  func(string) string
}

type styledLine struct {
	text string
	tag  string
}

type parsedLogEntry struct {
	Time    string
	Level   string
	Msg     string
	NodeID  string
	Service string
	Comp    string
	Event   string
	Peer    string
	From    string
	To      string
	MsgID   string
	ReqID   string
	SessKey string
	Err     string
	Extra   []string
	Raw     string
	IsJSON  bool
}

// peerFilterState holds the peers-tab filter: the applied expression (raw)
// and the in-progress edit buffer while the user types.
type peerFilterState struct {
	raw     string
	buf     string
	editing bool
}

type model struct {
	client         Client
	logs           LogProvider
	apiAddr        string
	timeout        time.Duration
	version        string
	logSource      string
	logLines       int
	width          int
	height         int
	layout         layout
	activeTab      int
	lastUpdated    time.Time
	snapshot       snapshot
	loading        bool
	errText        string
	cursors        [dashboardTabCount]int
	offsets        [dashboardTabCount]int
	logsFollowTail bool
	cfgState       configEditState
	peerFilter     peerFilterState
}

func (m *model) recalcLayout() {
	w := m.width
	h := m.height
	narrow := w < dashboardNarrowBreakpoint

	lo := layout{
		width:  w,
		height: h,
		narrow: narrow,
	}

	if narrow {
		lo.splitLeftW = w
		lo.splitRightW = w
		lo.logsLeftW = w
		lo.logsRightW = w
	} else {
		lo.splitLeftW = w * 3 / 5
		lo.splitRightW = w - lo.splitLeftW - 1
		lo.logsLeftW = w * 7 / 10
		lo.logsRightW = w - lo.logsLeftW - 1
	}

	lo.cardWidth = maxInt(18, (w-3)/4)
	m.layout = lo
}
