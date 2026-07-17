#!/bin/sh
# Entrypoint for the per-ticket Claude Code container.
#
# The claude-auth volume is mounted at $HOME/.claude so OAuth credentials persist
# across restarts. That volume shadows anything baked into the image at that path,
# so on every start we deterministically seed the image-baked (pinned) plugins and
# settings from the preload stash into the volume — WITHOUT touching the persisted
# credentials. Result: plugins/LSP/skills are reproducible from the image; only the
# account login is stateful. Nothing about this project has to be maintained on the
# host.
set -eu

PRELOAD="${CLAUDE_PRELOAD:-/opt/claude-preload}"
DEST="${HOME:-/root}/.claude"
mkdir -p "$DEST"

# Plugins (marketplaces + installed + catalog): always refreshed from the image.
if [ -d "$PRELOAD/plugins" ]; then
	rm -rf "$DEST/plugins"
	cp -a "$PRELOAD/plugins" "$DEST/plugins"
fi

# settings.json (enabledPlugins etc.): refreshed from the image. Credentials live
# in the separate $DEST/.credentials.json and are never touched here.
if [ -f "$PRELOAD/settings.json" ]; then
	cp -a "$PRELOAD/settings.json" "$DEST/settings.json"
fi

# The main config (~/.claude.json) normally lives OUTSIDE ~/.claude, so it isn't
# in the volume and would be lost on every start (re-triggering the folder-trust
# prompt and printing a "config not found" warning). Symlink it into the volume so
# trust/onboarding state persists deterministically across restarts.
CONFIG_HOME="${HOME:-/root}/.claude.json"
if [ ! -L "$CONFIG_HOME" ]; then
	rm -f "$CONFIG_HOME"
	ln -s "$DEST/.claude.json" "$CONFIG_HOME"
fi
[ -f "$DEST/.claude.json" ] || echo '{}' > "$DEST/.claude.json"

exec "$@"
