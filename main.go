package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.design/x/clipboard"
)

var errNotFound = errors.New("no image found")

func main() {
	opts, showHelp, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		printUsage(os.Stderr)
		os.Exit(2)
	}
	if showHelp {
		printUsage(os.Stdout)
		return
	}

	result, err := run(opts)
	if err == nil {
		fmt.Println(result.source)
		fmt.Println(result.tempPath)
		return
	}
	if errors.Is(err, errNotFound) {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(2)
}

type options struct {
	useDownloads  bool
	verbose       bool
	clipboardOnly bool
}

func parseArgs(args []string) (options, bool, error) {
	var opts options
	var help bool
	fs := flag.NewFlagSet("screenshot-agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.clipboardOnly, "clipboard-only", false, "use clipboard only (no file fallback)")
	fs.BoolVar(&opts.useDownloads, "downloads", false, "search Downloads instead of Desktop")
	fs.BoolVar(&opts.verbose, "verbose", false, "verbose logging to stderr")
	fs.BoolVar(&opts.verbose, "v", false, "verbose logging to stderr")
	fs.BoolVar(&help, "help", false, "show help and exit")
	fs.BoolVar(&help, "h", false, "show help and exit")
	if err := fs.Parse(args); err != nil {
		return opts, false, err
	}
	return opts, help, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: screenshot-agent [options]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Print two lines: source (clipboard or original file path) and")
	fmt.Fprintln(w, "the temp path of a PNG/JPG/JPEG image from Desktop or Downloads.")
	fmt.Fprintln(w, "Desktop files are copied to temp and trashed; Downloads are moved.")
	fmt.Fprintln(w, "Exits 1 if nothing is found.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "options:")
	fmt.Fprintln(w, "  -h, -help, --help    show this help and exit")
	fmt.Fprintln(w, "  --clipboard-only      use clipboard only (no file fallback)")
	fmt.Fprintln(w, "  --downloads          search Downloads instead of Desktop")
	fmt.Fprintln(w, "  -v, --verbose         verbose logging to stderr")
}

type result struct {
	source   string
	tempPath string
}

func run(opts options) (result, error) {
	clipboardCandidate, clipboardErr := readClipboardImage()
	if opts.clipboardOnly {
		if clipboardErr == nil {
			logf(opts, "selected clipboard candidate (clipboard-only)")
			return handleClipboardCandidate(clipboardCandidate)
		}
		if clipboardErr != nil && !errors.Is(clipboardErr, errNotFound) {
			return result{}, clipboardErr
		}
		return result{}, errNotFound
	}

	fileCandidate, fileErr := findFallbackImage(opts.useDownloads)
	now := time.Now()

	if clipboardErr == nil && fileErr == nil {
		if preferFileCandidate(fileCandidate, now) {
			logf(opts, "selected file candidate: %s", fileCandidate.path)
			return handleFileCandidate(fileCandidate, opts)
		}
		logf(opts, "selected clipboard candidate")
		return handleClipboardCandidate(clipboardCandidate)
	}
	if clipboardErr == nil {
		logf(opts, "selected clipboard candidate (file missing)")
		return handleClipboardCandidate(clipboardCandidate)
	}
	if fileErr == nil {
		logf(opts, "selected file candidate (clipboard missing): %s", fileCandidate.path)
		return handleFileCandidate(fileCandidate, opts)
	}
	if fileErr != nil && !errors.Is(fileErr, errNotFound) {
		return result{}, fileErr
	}
	if clipboardErr != nil && !errors.Is(clipboardErr, errNotFound) {
		return result{}, clipboardErr
	}
	return result{}, errNotFound
}

func logf(opts options, format string, args ...any) {
	if !opts.verbose {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func preferFileCandidate(candidate fileCandidate, now time.Time) bool {
	if candidate.modTime.IsZero() {
		return false
	}
	if candidate.modTime.After(now) {
		return true
	}
	return now.Sub(candidate.modTime) <= 30*time.Second
}

func handleClipboardCandidate(candidate clipboardCandidate) (result, error) {
	tempPath, err := writeClipboardToTemp(candidate.data)
	if err != nil {
		return result{}, err
	}
	return result{source: "clipboard", tempPath: tempPath}, nil
}

func handleFileCandidate(candidate fileCandidate, opts options) (result, error) {
	source := candidate.path
	if opts.useDownloads {
		logf(opts, "moving Downloads file to temp: %s", candidate.path)
		tempPath, err := moveImageToTemp(candidate.path)
		if err != nil {
			return result{}, err
		}
		return result{source: source, tempPath: tempPath}, nil
	}
	logf(opts, "copying Desktop file to temp and trashing: %s", candidate.path)
	tempPath, err := copyImageToTemp(candidate.path)
	if err != nil {
		return result{}, err
	}
	if err := trashFile(candidate.path); err != nil {
		os.Remove(tempPath)
		return result{}, err
	}
	return result{source: source, tempPath: tempPath}, nil
}

type clipboardCandidate struct {
	data []byte
}

func readClipboardImage() (clipboardCandidate, error) {
	if err := clipboard.Init(); err != nil {
		return clipboardCandidate{}, err
	}
	data := clipboard.Read(clipboard.FmtImage)
	if len(data) == 0 {
		return clipboardCandidate{}, errNotFound
	}
	return clipboardCandidate{data: data}, nil
}

func writeClipboardToTemp(data []byte) (string, error) {
	file, err := os.CreateTemp(os.TempDir(), "clipboard-*.png")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return filepath.Abs(path)
}

type fileCandidate struct {
	path    string
	modTime time.Time
}

func findFallbackImage(useDownloads bool) (fileCandidate, error) {
	fallbackDir, err := locateFallbackDir(useDownloads)
	if err != nil {
		return fileCandidate{}, err
	}
	return latestImage(fallbackDir)
}

func copyImageToTemp(src string) (string, error) {
	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" {
		ext = ".png"
	}
	tempPath, err := tempMovePath("image-*" + ext)
	if err != nil {
		return "", err
	}
	if err := copyFile(src, tempPath); err != nil {
		os.Remove(tempPath)
		return "", err
	}
	return filepath.Abs(tempPath)
}

func moveImageToTemp(src string) (string, error) {
	ext := strings.ToLower(filepath.Ext(src))
	if ext == "" {
		ext = ".png"
	}
	tempPath, err := tempMovePath("image-*" + ext)
	if err != nil {
		return "", err
	}
	if err := moveFile(src, tempPath); err != nil {
		os.Remove(tempPath)
		return "", err
	}
	return filepath.Abs(tempPath)
}

func locateFallbackDir(useDownloads bool) (string, error) {
	if useDownloads {
		return locateDownloads()
	}
	return locateDesktop()
}

func locateDesktop() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	defaultDesktop := filepath.Join(home, "Desktop")
	if info, err := os.Stat(defaultDesktop); err == nil && info.IsDir() {
		return defaultDesktop, nil
	}
	if runtime.GOOS == "linux" {
		if dir := xdgUserDir(home, "DESKTOP"); dir != "" {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return dir, nil
			}
		}
	}
	return "", errNotFound
}

func locateDownloads() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	defaultDownloads := filepath.Join(home, "Downloads")
	if info, err := os.Stat(defaultDownloads); err == nil && info.IsDir() {
		return defaultDownloads, nil
	}
	if runtime.GOOS == "linux" {
		if dir := xdgUserDir(home, "DOWNLOAD"); dir != "" {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return dir, nil
			}
		}
	}
	return "", errNotFound
}

func xdgUserDir(home, key string) string {
	configPath := filepath.Join(home, ".config", "user-dirs.dirs")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	prefix := "XDG_" + key + "_DIR="
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimPrefix(line, prefix)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		value = strings.ReplaceAll(value, "${HOME}", home)
		value = strings.ReplaceAll(value, "$HOME", home)
		if strings.HasPrefix(value, "~") {
			value = filepath.Join(home, strings.TrimPrefix(value, "~"))
		}
		if value == "" {
			return ""
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(home, value)
		}
		return filepath.Clean(value)
	}
	return ""
}

func latestImage(dir string) (fileCandidate, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fileCandidate{}, errNotFound
		}
		return fileCandidate{}, err
	}

	var latestTagged fileCandidate
	var latestTaggedTime int64
	var latestAny fileCandidate
	var latestAnyTime int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !hasImageExt(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		mod := info.ModTime().UnixNano()
		candidate := fileCandidate{
			path:    filepath.Join(dir, name),
			modTime: info.ModTime(),
		}
		if isScreenshotName(name) {
			if latestTagged.path == "" || mod > latestTaggedTime {
				latestTagged = candidate
				latestTaggedTime = mod
			}
			continue
		}
		if latestAny.path == "" || mod > latestAnyTime {
			latestAny = candidate
			latestAnyTime = mod
		}
	}
	if latestTagged.path != "" {
		return latestTagged, nil
	}
	if latestAny.path != "" {
		return latestAny, nil
	}
	return fileCandidate{}, errNotFound
}

func hasImageExt(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg":
		return true
	default:
		return false
	}
}

func isScreenshotName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "screenshot") || strings.Contains(lower, "screen shot")
}

func tempMovePath(pattern string) (string, error) {
	file, err := os.CreateTemp(os.TempDir(), pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if errors.Is(err, syscall.EXDEV) {
		return copyAndRemove(src, dst)
	} else {
		return err
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}

func copyAndRemove(src, dst string) error {
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func trashFile(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return trashDarwin(absPath)
	case "linux":
		return trashLinux(absPath)
	default:
		return fmt.Errorf("trash unsupported on %s", runtime.GOOS)
	}
}

func trashDarwin(absPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	trashDir := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(trashDir, 0o700); err != nil {
		return err
	}
	name, err := uniqueTrashName(filepath.Base(absPath), trashDir, "")
	if err != nil {
		return err
	}
	dest := filepath.Join(trashDir, name)
	return moveFile(absPath, dest)
}

func trashLinux(absPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	trashRoot := filepath.Join(home, ".local", "share", "Trash")
	filesDir := filepath.Join(trashRoot, "files")
	infoDir := filepath.Join(trashRoot, "info")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		return err
	}

	name, err := uniqueTrashName(filepath.Base(absPath), filesDir, infoDir)
	if err != nil {
		return err
	}
	dest := filepath.Join(filesDir, name)
	if err := moveFile(absPath, dest); err != nil {
		return err
	}

	infoPath := filepath.Join(infoDir, name+".trashinfo")
	info := trashInfoContent(absPath, time.Now())
	if err := os.WriteFile(infoPath, []byte(info), 0o600); err != nil {
		_ = moveFile(dest, absPath)
		return err
	}
	return nil
}

func uniqueTrashName(base, filesDir, infoDir string) (string, error) {
	if base == "" {
		return "", errors.New("empty trash name")
	}
	if !trashNameExists(base, filesDir, infoDir) {
		return base, nil
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i < 10000; i++ {
		name := fmt.Sprintf("%s.%d%s", stem, i, ext)
		if !trashNameExists(name, filesDir, infoDir) {
			return name, nil
		}
	}
	return "", errors.New("unable to find unique trash name")
}

func trashNameExists(name, filesDir, infoDir string) bool {
	if exists(filepath.Join(filesDir, name)) {
		return true
	}
	if infoDir == "" {
		return false
	}
	return exists(filepath.Join(infoDir, name+".trashinfo"))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	return !os.IsNotExist(err)
}

func trashInfoContent(absPath string, deleted time.Time) string {
	return fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		trashEscapePath(absPath),
		deleted.Format("2006-01-02T15:04:05"),
	)
}

func trashEscapePath(path string) string {
	escaped := url.PathEscape(path)
	escaped = strings.ReplaceAll(escaped, "%2F", "/")
	escaped = strings.ReplaceAll(escaped, "%2f", "/")
	return escaped
}
