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

exec "$@"
