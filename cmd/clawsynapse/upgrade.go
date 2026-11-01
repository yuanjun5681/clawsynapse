package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	internalapi "clawsynapse/internal/api"
)

const (
	defaultUpgradeRepo = "yuanjun5681/clawsynapse"
	manifestRelPath    = ".clawsynapse/manifest.json"
	cliBinaryName      = "clawsynapse"
	daemonBinaryName   = "clawsynapsed"
)

var (
	upgradeDefaultClient = &http.Client{Timeout: 10 * time.Second}
	upgradeUserHomeDir   = os.UserHomeDir
	upgradeExecutable    = os.Executable
	upgradeLookPath      = exec.LookPath
)

type upgradeRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execUpgradeRunner struct{}

func (execUpgradeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

type upgradeManifest struct {
	Product        string   `json:"product"`
	Repo           string   `json:"repo"`
	Version        string   `json:"version"`
	InstallDir     string   `json:"installDir"`
	Components     []string `json:"components"`
	ServiceManager string   `json:"serviceManager,omitempty"`
}

type releaseInfo struct {
	TagName string `json:"tag_name"`
}

type upgradeCheckResult struct {
	Current upgradeCheckCurrent `json:"current"`
	Latest  string              `json:"latest"`
	Updates []upgradeCheckItem  `json:"updates,omitempty"`
	OK      bool                `json:"ok"`
}

type upgradeCheckCurrent struct {
	CLI    string `json:"cli,omitempty"`
	Daemon string `json:"daemon,omitempty"`
}

type upgradeCheckItem struct {
	Component string `json:"component"`
	Current   string `json:"current"`
	Target    string `json:"target"`
}

type upgradeDeps struct {
	runner     upgradeRunner
	httpClient *http.Client
	homeDir    func() (string, error)
	executable func() (string, error)
	lookPath   func(string) (string, error)
	apiAddr    string
	localCheck func(context.Context, string) (string, error)
	now        func() time.Time
}

type installState struct {
	manifest      upgradeManifest
	manifestFound bool
	repo          string
	installDir    string
	cliVersion    string
	daemonVersion string
	daemonPath    string
	components    []string
}

func runUpgrade(args []string, stdin io.Reader, stdout, stderr io.Writer, apiAddr, tokenFile string) error {
	deps := upgradeDeps{
		runner:     execUpgradeRunner{},
		httpClient: upgradeDefaultClient,
		homeDir:    upgradeUserHomeDir,
		executable: upgradeExecutable,
		lookPath:   upgradeLookPath,
		apiAddr:    apiAddr,
		localCheck: func(ctx context.Context, addr string) (string, error) {
			return fetchLocalDaemonVersionWithToken(ctx, addr, tokenFile)
		},
		now: time.Now,
	}
	return runUpgradeWithDeps(args, stdin, stdout, stderr, deps)
}

func runUpgradeWithDeps(args []string, stdin io.Reader, stdout, stderr io.Writer, deps upgradeDeps) error {
	action := "apply"
	if len(args) > 0 && args[0] == "check" {
		action = "check"
		args = args[1:]
	}

	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		printUpgradeHelp(stderr)
	}

	cliOnly := fs.Bool("cli", false, "upgrade CLI only")
	daemonOnly := fs.Bool("daemon", false, "upgrade daemon only")
	all := fs.Bool("all", false, "upgrade both CLI and daemon")
	targetVersion := fs.String("version", "latest", "target release tag")
	repo := fs.String("repo", "", "GitHub repo in OWNER/REPO form")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	asJSON := fs.Bool("json", false, "print structured JSON output for check")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return flag.ErrHelp
		}
		return err
	}

	rest := fs.Args()
	if len(rest) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(rest, " "))
	}

	selectionCount := 0
	for _, enabled := range []bool{*cliOnly, *daemonOnly, *all} {
		if enabled {
			selectionCount++
		}
	}
	if selectionCount > 1 {
		return errors.New("use only one of --cli, --daemon, or --all")
	}

	ctx := context.Background()
	state, err := inspectInstallState(ctx, deps, strings.TrimSpace(*repo))
	if err != nil {
		return err
	}

	latest := ""
	if action == "check" || strings.TrimSpace(*targetVersion) == "" || strings.TrimSpace(*targetVersion) == "latest" {
		latest, err = fetchLatestRelease(ctx, deps.httpClient, state.repo)
		if err != nil {
			return err
		}
	}

	if action == "check" {
		if *asJSON {
			return printUpgradeCheckJSON(stdout, state, latest)
		}
		printUpgradeCheck(stdout, state, latest)
		return nil
	}

	components, err := selectUpgradeComponents(state, *cliOnly, *daemonOnly, *all)
	if err != nil {
		return err
	}

	resolvedVersion := strings.TrimSpace(*targetVersion)
	if resolvedVersion == "" || resolvedVersion == "latest" {
		resolvedVersion = latest
	}

	if !*yes {
		if err := confirmUpgrade(stdin, stdout, state, resolvedVersion, components); err != nil {
			return err
		}
	} else {
		printUpgradePlan(stdout, state, resolvedVersion, components)
	}

	if err := runInstaller(ctx, deps, stdout, state, resolvedVersion, components); err != nil {
		return err
	}

	fmt.Fprintln(stdout, "Upgrade completed.")
	return nil
}

func inspectInstallState(ctx context.Context, deps upgradeDeps, requestedRepo string) (installState, error) {
	state := installState{
		repo:       defaultUpgradeRepo,
		cliVersion: version,
	}
	if requestedRepo != "" {
		state.repo = requestedRepo
	}

	if deps.localCheck != nil {
		if daemonVersion, err := deps.localCheck(ctx, deps.apiAddr); err == nil && daemonVersion != "" {
			state.daemonVersion = daemonVersion
		}
	}

	home, err := deps.homeDir()
	if err == nil && home != "" {
		manifestPath := filepath.Join(home, manifestRelPath)
		manifest, found, manifestErr := loadManifest(manifestPath)
		if manifestErr != nil {
			return installState{}, manifestErr
		}
		if found {
			state.manifest = manifest
			state.manifestFound = true
			if manifest.Repo != "" && requestedRepo == "" {
				state.repo = manifest.Repo
			}
			if manifest.InstallDir != "" {
				state.installDir = manifest.InstallDir
			}
		}
	}

	exePath, err := deps.executable()
	if err == nil && exePath != "" {
		exeDir := filepath.Dir(exePath)
		if state.installDir == "" {
			state.installDir = exeDir
		}
		candidate := filepath.Join(exeDir, daemonBinaryName)
		if daemonVersion, daemonErr := readBinaryVersion(ctx, deps.runner, candidate); daemonErr == nil {
			state.daemonPath = candidate
			if state.daemonVersion == "" {
				state.daemonVersion = daemonVersion
			}
		}
	}

	if state.daemonPath == "" && state.installDir != "" {
		candidate := filepath.Join(state.installDir, daemonBinaryName)
		if daemonVersion, daemonErr := readBinaryVersion(ctx, deps.runner, candidate); daemonErr == nil {
			state.daemonPath = candidate
			if state.daemonVersion == "" {
				state.daemonVersion = daemonVersion
			}
		}
	}

	if state.daemonPath == "" {
		if path, pathErr := deps.lookPath(daemonBinaryName); pathErr == nil {
			if daemonVersion, daemonErr := readBinaryVersion(ctx, deps.runner, path); daemonErr == nil {
				state.daemonPath = path
				if state.daemonVersion == "" {
					state.daemonVersion = daemonVersion
				}
				if state.installDir == "" {
					state.installDir = filepath.Dir(path)
				}
			}
		}
	}

	state.components = detectInstalledComponents(state)
	return state, nil
}

func fetchLocalDaemonVersion(ctx context.Context, apiAddr string) (string, error) {
	return fetchLocalDaemonVersionWithToken(ctx, apiAddr, "")
}

func fetchLocalDaemonVersionWithToken(ctx context.Context, apiAddr, tokenFile string) (string, error) {
	addr := strings.TrimSpace(apiAddr)
	options := []internalapi.ClientOption{}
	if tokenFile != "" {
		options = append(options, internalapi.WithTokenFile(tokenFile))
	}
	client := internalapi.NewClient(addr, 2*time.Second, options...)
	result, err := client.Get(ctx, "/v2/health")
	if err != nil {
		return "", err
	}
	versionValue, _ := result.Data["version"].(string)
	versionValue = strings.TrimSpace(versionValue)
	if versionValue == "" {
		return "", errors.New("health response missing daemon version")
	}
	return versionValue, nil
}

func loadManifest(path string) (upgradeManifest, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return upgradeManifest{}, false, nil
		}
		return upgradeManifest{}, false, fmt.Errorf("read manifest: %w", err)
	}

	var manifest upgradeManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return upgradeManifest{}, false, fmt.Errorf("parse manifest: %w", err)
	}
	return manifest, true, nil
}

func readBinaryVersion(ctx context.Context, runner upgradeRunner, path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	out, err := runner.Run(ctx, path, "version")
	if err != nil {
		return "", err
	}
	versionText := strings.TrimSpace(string(out))
	if versionText == "" {
		return "", errors.New("empty version output")
	}
	return versionText, nil
}

func detectInstalledComponents(state installState) []string {
	components := make([]string, 0, 2)
	if slices.Contains(state.manifest.Components, "cli") || state.cliVersion != "" {
		components = append(components, "cli")
	}
	if slices.Contains(state.manifest.Components, "daemon") || state.daemonVersion != "" {
		components = append(components, "daemon")
	}
	return components
}

func fetchLatestRelease(ctx context.Context, client *http.Client, repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "clawsynapse-upgrade")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("fetch latest release: unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var release releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decode latest release: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return "", errors.New("latest release did not include tag_name")
	}
	return strings.TrimSpace(release.TagName), nil
}

func printUpgradeCheck(stdout io.Writer, state installState, latest string) {
	fmt.Fprintln(stdout, "Current:")
	fmt.Fprintf(stdout, "  cli:    %s\n", fallbackVersion(state.cliVersion))
	fmt.Fprintf(stdout, "  daemon: %s\n", fallbackVersion(state.daemonVersion))
	fmt.Fprintln(stdout, "Latest:")
	fmt.Fprintf(stdout, "  release: %s\n", latest)
	fmt.Fprintln(stdout)

	available := false
	if state.cliVersion != "" && state.cliVersion != latest {
		available = true
	}
	if state.daemonVersion != "" && state.daemonVersion != latest {
		available = true
	}

	if !available {
		fmt.Fprintln(stdout, "Already up to date.")
		return
	}

	fmt.Fprintln(stdout, "Upgrade available:")
	if state.cliVersion != "" && state.cliVersion != latest {
		fmt.Fprintf(stdout, "  cli:    %s -> %s\n", state.cliVersion, latest)
	}
	if state.daemonVersion != "" && state.daemonVersion != latest {
		fmt.Fprintf(stdout, "  daemon: %s -> %s\n", state.daemonVersion, latest)
	}
}

func printUpgradeCheckJSON(stdout io.Writer, state installState, latest string) error {
	result := upgradeCheckResult{
		Current: upgradeCheckCurrent{
			CLI:    state.cliVersion,
			Daemon: state.daemonVersion,
		},
		Latest: latest,
		OK:     true,
	}
	if state.cliVersion != "" && state.cliVersion != latest {
		result.OK = false
		result.Updates = append(result.Updates, upgradeCheckItem{
			Component: "cli",
			Current:   state.cliVersion,
			Target:    latest,
		})
	}
	if state.daemonVersion != "" && state.daemonVersion != latest {
		result.OK = false
		result.Updates = append(result.Updates, upgradeCheckItem{
			Component: "daemon",
			Current:   state.daemonVersion,
			Target:    latest,
		})
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func fallbackVersion(value string) string {
	if strings.TrimSpace(value) == "" {
		return "not installed"
	}
	return value
}

func selectUpgradeComponents(state installState, cliOnly, daemonOnly, all bool) ([]string, error) {
	switch {
	case all:
		return []string{"cli", "daemon"}, nil
	case cliOnly:
		return []string{"cli"}, nil
	case daemonOnly:
		return []string{"daemon"}, nil
	case len(state.components) > 0:
		return append([]string(nil), state.components...), nil
	default:
		return []string{"cli"}, nil
	}
}

func printUpgradePlan(stdout io.Writer, state installState, targetVersion string, components []string) {
	fmt.Fprintf(stdout, "Target version: %s\n", targetVersion)
	fmt.Fprintf(stdout, "Components: %s\n", strings.Join(components, ", "))
	if state.installDir != "" {
		fmt.Fprintf(stdout, "Install dir: %s\n", state.installDir)
	}
	fmt.Fprintln(stdout)
}

func confirmUpgrade(stdin io.Reader, stdout io.Writer, state installState, targetVersion string, components []string) error {
	if stdin == nil {
		return errors.New("confirmation required, rerun with --yes")
	}
	printUpgradePlan(stdout, state, targetVersion, components)
	fmt.Fprint(stdout, "Proceed? [y/N]: ")
	reader := bufio.NewReader(stdin)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return errors.New("upgrade cancelled")
	}
	return nil
}

func runInstaller(ctx context.Context, deps upgradeDeps, stdout io.Writer, state installState, targetVersion string, components []string) error {
	scriptRef := "main"
	if targetVersion != "" && targetVersion != "latest" {
		scriptRef = targetVersion
	}
	scriptURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/scripts/install.sh", state.repo, scriptRef)

	scriptPath, err := downloadInstaller(ctx, deps.httpClient, scriptURL)
	if err != nil {
		return err
	}
	defer os.Remove(scriptPath)

	args := buildInstallerArgs(state, targetVersion, components)
	out, err := deps.runner.Run(ctx, "bash", append([]string{scriptPath}, args...)...)
	if len(out) > 0 {
		fmt.Fprint(stdout, string(out))
	}
	if err != nil {
		return fmt.Errorf("run installer: %w", err)
	}
	return nil
}

func downloadInstaller(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "clawsynapse-upgrade")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download installer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("download installer: unexpected status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	file, err := os.CreateTemp("", "clawsynapse-install-*.sh")
	if err != nil {
		return "", err
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return "", err
	}
	if err := file.Chmod(0o700); err != nil {
		return "", err
	}
	return file.Name(), nil
}

func buildInstallerArgs(state installState, targetVersion string, components []string) []string {
	args := make([]string, 0, 8)
	switch {
	case len(components) == 2:
		args = append(args, "--all")
	case len(components) == 1 && components[0] == "daemon":
		args = append(args, "--daemon")
	default:
		args = append(args, "--cli")
	}
	if state.installDir != "" {
		args = append(args, "--install-dir", state.installDir)
	}
	if targetVersion != "" {
		args = append(args, "--version", targetVersion)
	}
	if state.repo != "" {
		args = append(args, "--repo", state.repo)
	}
	return args
}

func printUpgradeHelp(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: clawsynapse upgrade [flags]")
	fmt.Fprintln(stderr, "       clawsynapse upgrade check [flags]")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Check for updates or apply an upgrade using the release installer.")
	fmt.Fprintln(stderr, "")
	fmt.Fprintln(stderr, "Flags:")
	fmt.Fprintln(stderr, "  --cli               upgrade CLI only")
	fmt.Fprintln(stderr, "  --daemon            upgrade daemon only")
	fmt.Fprintln(stderr, "  --all               upgrade both CLI and daemon")
	fmt.Fprintln(stderr, "  --version string    target release tag (default \"latest\")")
	fmt.Fprintln(stderr, "  --repo OWNER/REPO   override GitHub repo (default \"yuanjun5681/clawsynapse\")")
	fmt.Fprintln(stderr, "  --yes               skip confirmation prompt")
	fmt.Fprintln(stderr, "  --json              print structured JSON output for check")
}
