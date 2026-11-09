package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

// runContext uses explicit invocation identity, never cwd or prior chat content.
func runContext(ctx context.Context, client localAPIClient, args []string) (types.APIResult, error) {
	if len(args) == 0 || args[0] != "read" {
		return types.APIResult{}, fmt.Errorf("usage: clawsynapse context read [--history] [--after REVISION] [--limit COUNT]")
	}
	fs := flag.NewFlagSet("context read", flag.ContinueOnError)
	from := fs.String("from", os.Getenv("CLAWSYNAPSE_TARGET_BOT"), "original sending Bot (defaults to current execution)")
	requestID := fs.String("request-id", os.Getenv("CLAWSYNAPSE_REQUEST_ID"), "active original request (defaults to current execution)")
	runID := fs.String("run-id", os.Getenv("CLAWSYNAPSE_RUN_ID"), "active run (defaults to current execution)")
	after := fs.Uint64("after", 0, "read after this public revision")
	limit := fs.Int("limit", 32, fmt.Sprintf("maximum events, between 1 and %d", protocol.TopicReaderMaxEvents))
	history := fs.Bool("history", false, "include already-known public sources within this request's fixed cut")
	if err := fs.Parse(args[1:]); err != nil {
		return types.APIResult{}, err
	}
	if fs.NArg() != 0 {
		return types.APIResult{}, fmt.Errorf("context read accepts flags only")
	}
	if strings.TrimSpace(*from) == "" || strings.TrimSpace(*requestID) == "" || strings.TrimSpace(*runID) == "" {
		return types.APIResult{}, fmt.Errorf("context read requires current execution identity or explicit --from, --request-id and --run-id")
	}
	if *limit < 1 || *limit > protocol.TopicReaderMaxEvents {
		return types.APIResult{}, fmt.Errorf("limit must be between 1 and %d", protocol.TopicReaderMaxEvents)
	}
	query := url.Values{"from": {*from}, "runId": {*runID}, "after": {strconv.FormatUint(*after, 10)}, "limit": {strconv.Itoa(*limit)}}
	if *history {
		query.Set("view", "history")
	}
	return client.Get(ctx, "/requests/"+url.PathEscape(*requestID)+"/context?"+query.Encode())
}
