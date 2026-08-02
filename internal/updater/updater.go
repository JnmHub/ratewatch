package updater

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

const (
	defaultAPIURL = "https://api.github.com"
	defaultRepo   = "JnmHub/ratewatch"
	helperCommand = "__ratewatch_apply_update"
	maxAssetSize  = 200 << 20
)

type Manager struct {
	HTTPClient     *http.Client
	APIURL         string
	Repository     string
	ExecutablePath func() (string, error)
}

type Status struct {
	CurrentVersion  string    `json:"current_version"`
	LatestVersion   string    `json:"latest_version"`
	Commit          string    `json:"commit"`
	BuildTime       string    `json:"build_time"`
	OS              string    `json:"os"`
	Arch            string    `json:"arch"`
	PublishedAt     time.Time `json:"published_at,omitempty"`
	ReleaseURL      string    `json:"release_url,omitempty"`
	AssetName       string    `json:"asset_name,omitempty"`
	UpdateAvailable bool      `json:"update_available"`
	CanAutoUpdate   bool      `json:"can_auto_update"`
	Reason          string    `json:"reason,omitempty"`
	assetURL        string
	checksumURL     string
}

type InstallResult struct {
	Version    string `json:"version"`
	Message    string `json:"message"`
	Restarting bool   `json:"restarting"`
}

type release struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func New() *Manager {
	return &Manager{
		HTTPClient:     &http.Client{Timeout: 2 * time.Minute},
		APIURL:         defaultAPIURL,
		Repository:     defaultRepo,
		ExecutablePath: os.Executable,
	}
}

func (m *Manager) Check(ctx context.Context) (Status, error) {
	status := Status{CurrentVersion: Version, Commit: Commit, BuildTime: BuildTime, OS: runtime.GOOS, Arch: runtime.GOARCH}
	rel, err := m.latest(ctx)
	if err != nil {
		return status, err
	}
	status.LatestVersion = rel.TagName
	status.PublishedAt = rel.PublishedAt
	status.ReleaseURL = rel.HTMLURL
	status.UpdateAvailable = newer(rel.TagName, Version)
	assetName := binaryAssetName(runtime.GOOS, runtime.GOARCH)
	for _, asset := range rel.Assets {
		switch asset.Name {
		case assetName:
			status.AssetName, status.assetURL = asset.Name, asset.URL
		case "checksums.txt":
			status.checksumURL = asset.URL
		}
	}
	switch {
	case Version == "dev" || Version == "unknown" || Version == "":
		status.Reason = "当前是开发构建，请安装正式发行版后使用在线更新"
	case !status.UpdateAvailable:
		status.Reason = "当前已经是最新版本"
	case assetName == "":
		status.Reason = "当前系统或处理器架构暂无自动更新文件"
	case status.assetURL == "" || status.checksumURL == "":
		status.Reason = "最新发行版缺少当前系统文件或校验文件"
	case inContainer():
		status.Reason = "容器环境请更新镜像并重新创建容器，不能在容器内覆盖程序"
	default:
		status.CanAutoUpdate = true
	}
	return status, nil
}

func (m *Manager) Install(ctx context.Context) (InstallResult, error) {
	status, err := m.Check(ctx)
	if err != nil {
		return InstallResult{}, err
	}
	if !status.CanAutoUpdate {
		if status.Reason == "" {
			status.Reason = "当前版本不能自动更新"
		}
		return InstallResult{}, errors.New(status.Reason)
	}
	checksums, err := m.download(ctx, status.checksumURL, 2<<20)
	if err != nil {
		return InstallResult{}, fmt.Errorf("下载校验文件失败: %w", err)
	}
	expected := checksumFor(checksums, status.AssetName)
	if expected == "" {
		return InstallResult{}, errors.New("校验文件中没有当前系统的二进制记录")
	}
	binary, err := m.download(ctx, status.assetURL, maxAssetSize)
	if err != nil {
		return InstallResult{}, fmt.Errorf("下载新版程序失败: %w", err)
	}
	actualBytes := sha256.Sum256(binary)
	actual := hex.EncodeToString(actualBytes[:])
	if !strings.EqualFold(expected, actual) {
		return InstallResult{}, errors.New("新版程序 SHA-256 校验失败，已拒绝安装")
	}
	executable, err := m.ExecutablePath()
	if err != nil {
		return InstallResult{}, fmt.Errorf("无法确定当前程序位置: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return InstallResult{}, err
	}
	staged := filepath.Join(filepath.Dir(executable), "."+filepath.Base(executable)+"."+safeTag(status.LatestVersion)+".next")
	if err = writeAtomic(staged, binary, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("无法写入新版程序: %w", err)
	}
	if err = launchHelper(executable, staged); err != nil {
		_ = os.Remove(staged)
		return InstallResult{}, fmt.Errorf("无法启动更新助手: %w", err)
	}
	return InstallResult{Version: status.LatestVersion, Message: "新版已校验完成，服务将自动重启", Restarting: true}, nil
}

func (m *Manager) latest(ctx context.Context) (release, error) {
	endpoint := strings.TrimRight(m.APIURL, "/") + "/repos/" + strings.Trim(m.Repository, "/") + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "RateWatch/"+Version)
	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("GitHub 返回 HTTP %d", resp.StatusCode)
	}
	var value release
	if err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&value); err != nil {
		return release{}, err
	}
	if value.TagName == "" || value.Draft || value.Prerelease {
		return release{}, errors.New("GitHub 没有可用的正式发行版")
	}
	return value, nil
}

func (m *Manager) download(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://127.0.0.1:") {
		return nil, errors.New("下载地址不是受支持的 HTTPS 地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "RateWatch/"+Version)
	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, errors.New("下载文件超过大小限制")
	}
	reader := io.LimitReader(resp.Body, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("下载文件超过大小限制")
	}
	return data, nil
}

func checksumFor(data []byte, assetName string) string {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == assetName {
			if _, err := hex.DecodeString(fields[0]); err == nil && len(fields[0]) == 64 {
				return strings.ToLower(fields[0])
			}
		}
	}
	return ""
}

func binaryAssetName(goos, goarch string) string {
	if goarch != "amd64" {
		return ""
	}
	switch goos {
	case "windows":
		return "ratewatch-windows-amd64.exe"
	case "linux":
		return "ratewatch-linux-amd64"
	default:
		return ""
	}
}

func newer(candidate, current string) bool {
	candidateParts, candidateOK := versionParts(candidate)
	currentParts, currentOK := versionParts(current)
	if !candidateOK || !currentOK {
		return false
	}
	for i := range candidateParts {
		if candidateParts[i] != currentParts[i] {
			return candidateParts[i] > currentParts[i]
		}
	}
	return false
}

func versionParts(value string) ([3]int, bool) {
	var out [3]int
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	value, _, _ = strings.Cut(value, "-")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return out, false
	}
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return out, false
		}
		out[index] = number
	}
	return out, true
}

func safeTag(value string) string {
	var result strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '-' || char == '_' {
			result.WriteRune(char)
		}
	}
	if result.Len() == 0 {
		return "update"
	}
	return result.String()
}

func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	text := strings.ToLower(string(data))
	return strings.Contains(text, "docker") || strings.Contains(text, "kubepods") || strings.Contains(text, "containerd")
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	temporary := path + ".download"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	_ = os.Remove(path)
	return os.Rename(temporary, path)
}

func copyFile(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func launchHelper(executable, staged string) error {
	directory := filepath.Dir(executable)
	helper := filepath.Join(directory, "."+filepath.Base(executable)+".updater-"+strconv.Itoa(os.Getpid()))
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(helper), ".exe") {
		helper += ".exe"
	}
	if err := copyFile(executable, helper, 0o755); err != nil {
		return err
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		workingDirectory = directory
	}
	arguments := []string{helperCommand, strconv.Itoa(os.Getpid()), staged, executable, workingDirectory}
	arguments = append(arguments, os.Args[1:]...)
	command := exec.Command(helper, arguments...)
	command.Dir = directory
	command.Env = os.Environ()
	logFile, err := os.OpenFile(executable+".update.log", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err == nil {
		command.Stdout = logFile
		command.Stderr = logFile
		defer logFile.Close()
	}
	if err = command.Start(); err != nil {
		_ = os.Remove(helper)
		return err
	}
	return command.Process.Release()
}

func RunHelper(arguments []string) bool {
	if len(arguments) < 6 || arguments[1] != helperCommand {
		return false
	}
	parentPID, err := strconv.Atoi(arguments[2])
	if err != nil || parentPID <= 0 {
		return true
	}
	staged, target, workingDirectory := arguments[3], arguments[4], arguments[5]
	originalArguments := append([]string(nil), arguments[6:]...)
	if err = applyUpdate(parentPID, staged, target, workingDirectory, originalArguments); err != nil {
		fmt.Fprintln(os.Stderr, "更新失败:", err)
	}
	return true
}

func applyUpdate(parentPID int, staged, target, workingDirectory string, arguments []string) error {
	if !waitProcessExit(parentPID, 90*time.Second) {
		return errors.New("等待旧版本退出超时")
	}
	backup := target + ".previous"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("备份旧版本失败: %w", err)
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("替换程序失败: %w", err)
	}
	_ = os.Chmod(target, 0o755)
	command := exec.Command(target, arguments...)
	command.Dir = workingDirectory
	command.Env = os.Environ()
	if err := command.Start(); err != nil {
		_ = os.Remove(target)
		_ = os.Rename(backup, target)
		return fmt.Errorf("启动新版本失败: %w", err)
	}
	return command.Process.Release()
}

func CleanupHelpers() {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	pattern := filepath.Join(filepath.Dir(executable), "."+filepath.Base(executable)+".updater-*")
	if runtime.GOOS == "windows" {
		pattern += ".exe"
	}
	time.Sleep(2 * time.Second)
	matches, _ := filepath.Glob(pattern)
	for _, match := range matches {
		_ = os.Remove(match)
	}
}
