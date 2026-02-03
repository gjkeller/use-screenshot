---
name: latest-screenshot
description: Retrieves the latest screenshot or downloaded image using the screenshot-agent binary. Use when the user asks to check the latest screenshot, clipboard image, or a recent downloaded image.
---

# Latest Screenshot

## Trigger
When the user asks to check the latest screenshot, clipboard image, or recent downloaded image.

## Install (one-time)
```bash
go install github.com/gjkeller/screenshot-agent@latest
```
Ensure `~/go/bin` (or `$(go env GOPATH)/bin`) is on `PATH`.

## Usage
- Desktop: `screenshot-agent`
- Downloads: `screenshot-agent --downloads`
- Clipboard only: `screenshot-agent --clipboard-only`
- Output is two lines:
  1. source (`clipboard` or original file path)
  2. temp file path (PNG/JPG/JPEG)

## Agent pattern
```bash
out="$(screenshot-agent)"
tmp="$(printf "%s\n" "$out" | sed -n '2p')"
```
If `tmp` is empty, treat as not found.

## Notes
- Desktop files are copied to temp then trashed.
- Downloads files are moved to temp (not trashed).
- Clipboard has no copy timestamp; the tool prefers a file modified within ~30s, otherwise clipboard.
- Linux: requires X11 dev packages for clipboard (e.g. `libx11-dev`).

## Fallback (if binary missing)
```bash
tmp="$(mktemp "${TMPDIR:-/tmp}/clip-XXXXXX.png")" && ( command -v pngpaste >/dev/null && pngpaste "$tmp" || command -v osascript >/dev/null && osascript -e 'set theData to (the clipboard as «class PNGf»)' -e 'set theFile to POSIX file "'"$tmp"'"' -e 'set theFileRef to open for access theFile with write permission' -e 'set eof of theFileRef to 0' -e 'write theData to theFileRef' -e 'close access theFileRef' || command -v wl-paste >/dev/null && wl-paste --type image/png > "$tmp" || command -v xclip >/dev/null && xclip -selection clipboard -t image/png -o > "$tmp" ) && [ -s "$tmp" ] && printf "clipboard\n%s\n" "$tmp"
```
