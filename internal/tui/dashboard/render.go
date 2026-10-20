package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

func renderMetricPanel(title, value, detail string, width int) string {
	detailTag := dashboardTagMuted
	valueTag := dashboardTagAccent
	if title == "NATS" {
		if strings.EqualFold(detail, "connected") || strings.EqualFold(value, "connected") {
			valueTag = dashboardTagGood
		} else {
			valueTag = dashboardTagBad
		}
	}
	lines := []string{
		taggedLine(valueTag, "  "+value),
		taggedLine(detailTag, "  "+detail),
	}
	return renderPanelWithTheme(title, width, 5, lines, dashboardMetricTheme())
}

func renderPanel(title string, width, height int, lines []string) string {
	return renderPanelWithTheme(title, width, height, lines, dashboardDefaultTheme())
}

func panelHeightForLines(width int, lines []string) int {
	width = maxInt(width, 8)
	innerWidth := width - 2
	return maxInt(3, measureWrappedLineCount(innerWidth, lines)+2)
}

func renderPanelWithTheme(title string, width, height int, lines []string, theme panelTheme) string {
	width = maxInt(width, 8)
	height = maxInt(height, 3)

	innerWidth := width - 2
	contentHeight := height - 2
	title = truncateRight(title, maxInt(1, innerWidth-4))

	titleStr := " " + title + " "
	titleVisLen := displayWidth(titleStr)
	remainH := maxInt(0, innerWidth-titleVisLen-1)
	top := theme.border(boxTL+boxH) + theme.title(titleStr) + theme.border(strings.Repeat(boxH, remainH)+boxTR)
	bottom := theme.border(boxBL + strings.Repeat(boxH, innerWidth) + boxBR)

	expanded := make([]styledLine, 0, len(lines))
	for _, rawLine := range lines {
		expanded = append(expanded, expandTaggedLine(rawLine, innerWidth)...)
	}

	if len(expanded) > contentHeight {
		expanded = expanded[:contentHeight]
		if contentHeight > 0 {
			expanded[contentHeight-1].text = truncateRight(expanded[contentHeight-1].text, maxInt(1, innerWidth-3)) + "..."
		}
	}

	body := make([]string, 0, height)
	body = append(body, top)
	for i := 0; i < contentHeight; i++ {
		line := styledLine{}
		if i < len(expanded) {
			line = expanded[i]
		}
		content := padRightVisible(truncateRight(line.text, innerWidth), innerWidth)
		content = dashboardApplyLineStyle(line.tag, content)
		body = append(body, theme.border(boxV)+content+theme.border(boxV))
	}
	body = append(body, bottom)
	return strings.Join(body, "\n")
}

func joinHorizontal(blocks []string, gap int) string {
	if len(blocks) == 0 {
		return ""
	}
	if len(blocks) == 1 {
		return blocks[0]
	}

	parsed := make([][]string, len(blocks))
	maxLines := 0
	widths := make([]int, len(blocks))
	for i, block := range blocks {
		parsed[i] = strings.Split(block, "\n")
		if len(parsed[i]) > maxLines {
			maxLines = len(parsed[i])
		}
		for _, line := range parsed[i] {
			if visibleLen(line) > widths[i] {
				widths[i] = visibleLen(line)
			}
		}
	}

	var out []string
	sep := strings.Repeat(" ", gap)
	for row := 0; row < maxLines; row++ {
		parts := make([]string, 0, len(parsed))
		for col := range parsed {
			line := ""
			if row < len(parsed[col]) {
				line = parsed[col][row]
			}
			parts = append(parts, padRightVisible(line, widths[col]))
		}
		out = append(out, strings.Join(parts, sep))
	}
	return strings.Join(out, "\n")
}

func measureWrappedLineCount(width int, lines []string) int {
	width = maxInt(width, 1)
	total := 0
	for _, rawLine := range lines {
		total += len(expandTaggedLine(rawLine, width))
	}
	return total
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

func ensureVisible(offset, cursor, total, visible int) int {
	if total <= 0 || visible <= 0 {
		return 0
	}
	maxOffset := maxInt(0, total-visible)
	offset = clampInt(offset, 0, maxOffset)
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+visible {
		offset = cursor - visible + 1
	}
	return clampInt(offset, 0, maxOffset)
}

type peerColumns struct {
	nodeIDWidth  int
	authWidth    int
	trustWidth   int
	adapterWidth int
}

func peerColumnsForWidth(width int, peers []types.Peer) peerColumns {
	const (
		peerPrefixWidth     = 1
		peerDotColumnsWidth = 2
		peerGapCount        = 6
		minNodeIDWidth      = 12
		minAdapterWidth     = 8
	)

	width = maxInt(width, 40)
	authWidth := maxDisplayWidth("auth", "authenticated", "pending", "rejected", "expired")
	trustWidth := maxDisplayWidth("trust", "trusted", "pending", "rejected", "revoked", "none")
	nodeIDWidth := displayWidth("nodeId")
	adapterWidth := displayWidth("adapter/version")
	for _, peer := range peers {
		nodeIDWidth = maxInt(nodeIDWidth, displayWidth(peer.NodeID))
		adapterWidth = maxInt(adapterWidth, displayWidth(peerAdapterLabel(peer)))
	}

	fixedWidth := peerPrefixWidth + peerDotColumnsWidth + peerGapCount + authWidth + trustWidth
	remaining := maxInt(minNodeIDWidth+minAdapterWidth, width-fixedWidth)
	if nodeIDWidth > remaining-minAdapterWidth {
		nodeIDWidth = maxInt(minNodeIDWidth, remaining-minAdapterWidth)
	}
	adapterWidth = maxInt(1, remaining-nodeIDWidth)

	return peerColumns{
		nodeIDWidth:  nodeIDWidth,
		authWidth:    authWidth,
		trustWidth:   trustWidth,
		adapterWidth: adapterWidth,
	}
}

func formatPeerListHeader(cols peerColumns) string {
	return strings.Join([]string{
		padRight(" ", 1),
		padRight(truncateRight("nodeId", cols.nodeIDWidth), cols.nodeIDWidth),
		padRight("", 1),
		padRight(truncateRight("auth", cols.authWidth), cols.authWidth),
		padRight("", 1),
		padRight(truncateRight("trust", cols.trustWidth), cols.trustWidth),
		truncateRight("adapter/version", cols.adapterWidth),
	}, " ")
}

func formatPeerRow(prefix string, peer types.Peer, cols peerColumns) string {
	authDot := peerAuthDot(peer.AuthStatus)
	trustDot := peerTrustDot(peer.TrustStatus)
	return strings.Join([]string{
		padRight(truncateRight(prefix, 1), 1),
		padRight(truncateRight(fallbackString(peer.NodeID, "-"), cols.nodeIDWidth), cols.nodeIDWidth),
		padRight(authDot, 1),
		padRight(truncateRight(fallbackString(peer.AuthStatus, "unknown"), cols.authWidth), cols.authWidth),
		padRight(trustDot, 1),
		padRight(truncateRight(fallbackString(peer.TrustStatus, "none"), cols.trustWidth), cols.trustWidth),
		truncateRight(peerAdapterLabel(peer), cols.adapterWidth),
	}, " ")
}

func peerAdapterLabel(peer types.Peer) string {
	adapter := fallbackString(peer.AgentProduct, "-")
	if peer.Version != "" {
		adapter = adapter + " " + peer.Version
	}
	return adapter
}

func peerAuthDot(status string) string {
	switch status {
	case types.AuthAuthenticated:
		return dotFull
	case types.AuthPending:
		return dotHalf
	case types.AuthRejected, types.AuthExpired:
		return dotEmpty
	default:
		return dotEmpty
	}
}

func peerTrustDot(status string) string {
	switch status {
	case types.TrustTrusted:
		return dotFull
	case types.TrustPending:
		return dotHalf
	case types.TrustRejected, types.TrustRevoked:
		return dotEmpty
	default:
		return dotEmpty
	}
}

type messageColumns struct {
	typeWidth    int
	fromWidth    int
	toWidth      int
	tsWidth      int
	previewWidth int
}

func messageColumnsForWidth(width int) messageColumns {
	width = maxInt(width, 32)
	gapCount := 5
	prefixWidth := 1
	tsWidth := 8
	usable := width - prefixWidth - gapCount - tsWidth
	if usable < 24 {
		usable = 24
	}

	typeWidth := clampInt(usable/4, 10, 18)
	fromWidth := clampInt(usable/6, 8, 14)
	toWidth := clampInt(usable/6, 8, 14)
	previewWidth := usable - typeWidth - fromWidth - toWidth

	if previewWidth < 12 {
		deficit := 12 - previewWidth
		typeWidth = maxInt(8, typeWidth-deficit)
		previewWidth = usable - typeWidth - fromWidth - toWidth
	}
	if previewWidth < 12 {
		deficit := 12 - previewWidth
		fromWidth = maxInt(6, fromWidth-deficit)
		previewWidth = usable - typeWidth - fromWidth - toWidth
	}
	if previewWidth < 12 {
		deficit := 12 - previewWidth
		toWidth = maxInt(6, toWidth-deficit)
		previewWidth = usable - typeWidth - fromWidth - toWidth
	}
	if previewWidth < 8 {
		previewWidth = 8
	}

	return messageColumns{
		typeWidth:    typeWidth,
		fromWidth:    fromWidth,
		toWidth:      toWidth,
		tsWidth:      tsWidth,
		previewWidth: previewWidth,
	}
}

func formatMessageListHeader(cols messageColumns) string {
	return formatMessageColumns(" ", "type", "from", "to", "ts", "preview", cols)
}

func formatMessageRow(prefix string, item protocol.MessageEnvelope, cols messageColumns) string {
	preview := normalizeSingleLine(messagePreview(item))
	if preview == "" {
		preview = "-"
	}
	return formatMessageColumns(
		prefix,
		compactType(item.Type),
		fallbackString(item.From, "-"),
		fallbackString(item.To, "-"),
		formatShortTime(item.Ts),
		preview,
		cols,
	)
}

func messagePreview(item protocol.MessageEnvelope) string {
	if item.Payload.Text != "" {
		return item.Payload.Text
	}
	if item.Payload.Instruction != "" {
		return item.Payload.Instruction
	}
	return item.Payload.Code
}

func formatMessageColumns(prefix, msgType, from, to, ts, preview string, cols messageColumns) string {
	return strings.Join([]string{
		padRight(truncateRight(prefix, 1), 1),
		padRight(truncateRight(msgType, cols.typeWidth), cols.typeWidth),
		padRight(truncateRight(from, cols.fromWidth), cols.fromWidth),
		padRight(truncateRight(to, cols.toWidth), cols.toWidth),
		padRight(truncateRight(ts, cols.tsWidth), cols.tsWidth),
		truncateRight(preview, cols.previewWidth),
	}, " ")
}

func compactType(v string) string {
	v = fallbackString(v, "unknown")
	parts := strings.Split(v, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[len(parts)-1]
	}
	return v
}

func formatShortTime(ts int64) string {
	if ts <= 0 {
		return "-"
	}
	return time.UnixMilli(ts).Format("15:04:05")
}

func formatUnixMilli(ts int64) string {
	if ts <= 0 {
		return "-"
	}
	return time.UnixMilli(ts).Format("2006-01-02 15:04:05")
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

func splitLogLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func recentMessageTypeLines(items []protocol.MessageEnvelope) []string {
	if len(items) == 0 {
		return []string{taggedLine(dashboardTagMuted, "  no recent messages")}
	}
	counts := map[string]int{}
	for _, item := range items {
		counts[compactType(item.Type)]++
	}
	type kv struct {
		key   string
		value int
	}
	pairs := make([]kv, 0, len(counts))
	for k, v := range counts {
		pairs = append(pairs, kv{key: k, value: v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].value == pairs[j].value {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].value > pairs[j].value
	})
	lines := make([]string, 0, minInt(5, len(pairs)))
	for i, item := range pairs {
		if i >= 5 {
			break
		}
		lines = append(lines, taggedLine(dashboardTagAccent, fmt.Sprintf("  %s: %d", item.key, item.value)))
	}
	return lines
}

func formatMetadata(meta map[string]any) []string {
	keys := make([]string, 0, len(meta))
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, taggedLine(dashboardTagMuted, fmt.Sprintf("  %s = %v", key, meta[key])))
	}
	return lines
}

func countPeersByAuth(peers []types.Peer, want string) int {
	count := 0
	for _, peer := range peers {
		if peer.AuthStatus == want {
			count++
		}
	}
	return count
}

func countPeersByTrust(peers []types.Peer, want string) int {
	count := 0
	for _, peer := range peers {
		if peer.TrustStatus == want {
			count++
		}
	}
	return count
}

func wrapLine(line string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	if line == "" {
		return []string{""}
	}
	var lines []string
	for displayWidth(line) > width {
		part := runewidth.Truncate(line, width, "")
		if part == "" {
			break
		}
		lines = append(lines, part)
		line = strings.TrimPrefix(line, part)
	}
	lines = append(lines, line)
	return lines
}

func expandTaggedLine(rawLine string, width int) []styledLine {
	tag, line := parseTaggedLine(rawLine)
	parts := splitPreservingEmptyLines(line)
	expanded := make([]styledLine, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			expanded = append(expanded, styledLine{text: "", tag: tag})
			continue
		}
		for _, wrapped := range wrapLine(part, width) {
			expanded = append(expanded, styledLine{text: wrapped, tag: tag})
		}
	}
	return expanded
}

func splitPreservingEmptyLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Split(text, "\n")
}

func normalizeSingleLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func dashboardDefaultTheme() panelTheme {
	return panelTheme{
		border: dashboardStyleBorder,
		title:  dashboardStylePanelTitle,
	}
}

func dashboardMetricTheme() panelTheme {
	return panelTheme{
		border: dashboardStyleMetricBorder,
		title:  dashboardStyleMetricTitle,
	}
}

func dashboardHeaderTheme() panelTheme {
	return panelTheme{
		border: dashboardStyleHeaderBorder,
		title:  dashboardStyleHeaderTitle,
	}
}

func taggedLine(tag, text string) string {
	if tag == "" {
		return text
	}
	return "[[" + tag + "]]" + text
}

func parseTaggedLine(line string) (string, string) {
	if !strings.HasPrefix(line, "[[") {
		return "", line
	}
	end := strings.Index(line, "]]")
	if end <= 2 {
		return "", line
	}
	return line[2:end], line[end+2:]
}

func dashboardApplyLineStyle(tag, text string) string {
	switch tag {
	case dashboardTagAccent:
		return dashboardStyleAccent(text)
	case dashboardTagMuted:
		return dashboardStyleMuted(text)
	case dashboardTagSection:
		return dashboardStyleSection(text)
	case dashboardTagGood:
		return dashboardStyleGood(text)
	case dashboardTagWarn:
		return dashboardStyleWarn(text)
	case dashboardTagBad:
		return dashboardStyleBad(text)
	case dashboardTagSelected:
		return dashboardStyleSelected(text)
	case dashboardTagDim:
		return dashboardStyleDim(text)
	case dashboardTagLogInfo:
		return dashboardStyleLogInfo(text)
	case dashboardTagLogWarn:
		return dashboardStyleLogWarn(text)
	case dashboardTagLogError:
		return dashboardStyleLogError(text)
	case dashboardTagLogDebug:
		return dashboardStyleLogDebug(text)
	default:
		return text
	}
}

func dashboardPeerAuthTag(status string) string {
	switch status {
	case types.AuthAuthenticated:
		return dashboardTagGood
	case types.AuthPending:
		return dashboardTagWarn
	case types.AuthRejected, types.AuthExpired:
		return dashboardTagBad
	default:
		return dashboardTagMuted
	}
}

func dashboardPeerTrustTag(status string) string {
	switch status {
	case types.TrustTrusted:
		return dashboardTagGood
	case types.TrustPending:
		return dashboardTagWarn
	case types.TrustRejected, types.TrustRevoked:
		return dashboardTagBad
	default:
		return dashboardTagMuted
	}
}

func dashboardStyle(code, text string) string {
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func dashboardStyleBorder(text string) string       { return dashboardStyle("38;5;37", text) }
func dashboardStyleMetricBorder(text string) string { return dashboardStyle("38;5;45", text) }
func dashboardStyleHeaderBorder(text string) string { return dashboardStyle("38;5;135", text) }
func dashboardStylePanelTitle(text string) string {
	return dashboardStyle("1;38;5;16;48;5;37", text)
}
func dashboardStyleMetricTitle(text string) string {
	return dashboardStyle("1;38;5;16;48;5;48", text)
}
func dashboardStyleHeaderTitle(text string) string {
	return dashboardStyle("1;38;5;255;48;5;55", text)
}
func dashboardStyleAccent(text string) string      { return dashboardStyle("1;38;5;45", text) }
func dashboardStyleMuted(text string) string       { return dashboardStyle("38;5;243", text) }
func dashboardStyleDim(text string) string         { return dashboardStyle("38;5;239", text) }
func dashboardStyleSection(text string) string     { return dashboardStyle("1;38;5;177", text) }
func dashboardStyleGood(text string) string        { return dashboardStyle("38;5;48", text) }
func dashboardStyleWarn(text string) string        { return dashboardStyle("38;5;214", text) }
func dashboardStyleBad(text string) string         { return dashboardStyle("1;38;5;197", text) }
func dashboardStyleFooter(text string) string      { return dashboardStyle("38;5;243", text) }
func dashboardStyleTabInactive(text string) string { return dashboardStyle("38;5;243", text) }
func dashboardStyleLogInfo(text string) string     { return dashboardStyle("38;5;45", text) }
func dashboardStyleLogWarn(text string) string     { return dashboardStyle("38;5;214", text) }
func dashboardStyleLogError(text string) string    { return dashboardStyle("1;38;5;197", text) }
func dashboardStyleLogDebug(text string) string    { return dashboardStyle("38;5;243", text) }
func dashboardStyleTabActive(text string) string {
	return dashboardStyle("1;38;5;255;48;5;30", text)
}
func dashboardStyleSelected(text string) string {
	return dashboardStyle("1;38;5;255;48;5;24", text)
}

func truncateRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if displayWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return runewidth.Truncate(s, width, "")
	}
	return runewidth.Truncate(s, width, "...")
}

func truncateRightVisible(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if visibleLen(s) <= width {
		return s
	}
	return runewidth.Truncate(stripANSI(s), width, "...")
}

func padRight(s string, width int) string {
	if displayWidth(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-displayWidth(s))
}

func padRightVisible(s string, width int) string {
	if visibleLen(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visibleLen(s))
}

func stripANSI(s string) string {
	return dashboardANSIRegexp.ReplaceAllString(s, "")
}

func visibleLen(s string) int {
	return displayWidth(stripANSI(s))
}

func displayWidth(s string) int {
	return runewidth.StringWidth(s)
}

func maxDisplayWidth(values ...string) int {
	width := 0
	for _, value := range values {
		width = maxInt(width, displayWidth(value))
	}
	return width
}

func clampInt(v, low, high int) int {
	if high < low {
		return low
	}
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func boolWord(v bool, yes, no string) string {
	if v {
		return yes
	}
	return no
}

func fallbackString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
