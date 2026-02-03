## use-screenshot skill

`use-screenshot` is a skill that fetches the latest screenshot or clipboard
image and returns a temp file path. It prints two lines:
the source (`clipboard` or original file path) and the temp file path.

## Install (skills.sh)

```bash
npx skills add gjkeller/use-screenshot
```

This repo contains the skill at `skills/use-screenshot/`.

## Local usage

```bash
node skills/use-screenshot/scripts/screenshot-agent.js --clipboard-only
node skills/use-screenshot/scripts/screenshot-agent.js --downloads
```

## Recommendation

Add a short blurb to your `~/AGENTS.md` so your agent knows how to invoke
`use-screenshot` and parse the two-line output.

## Requirements

- Node.js (no external npm deps)
- macOS: `osascript` (built-in) or `pngpaste` for clipboard images
- Linux: `wl-paste` or `xclip` for clipboard images

## Files

- `skills/use-screenshot/SKILL.md` — skill instructions and metadata
- `skills/use-screenshot/scripts/screenshot-agent.js` — bundled CLI
