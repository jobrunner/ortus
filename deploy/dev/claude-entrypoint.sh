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

# Wire git <-> gh so pushes/PRs work from inside the container (credential helper),
# and set a best-effort commit identity from the authenticated GitHub account when
# none is configured. All best-effort: skipped cleanly when gh isn't logged in.
if command -v gh >/dev/null 2>&1; then
	gh auth setup-git >/dev/null 2>&1 || true
	if [ -z "$(git config --global user.email 2>/dev/null)" ] && gh auth status >/dev/null 2>&1; then
		_login=$(gh api user -q .login 2>/dev/null || true)
		if [ -n "$_login" ]; then
			git config --global user.name "$(gh api user -q '.name // .login' 2>/dev/null || echo "$_login")"
			git config --global user.email "$(gh api user -q '.email // empty' 2>/dev/null || echo "${_login}@users.noreply.github.com")"
		fi
	fi
fi

# Stufe 2 — optional: run Remote Control as the container's MAIN process so
# `restart: unless-stopped` revives it after a Docker/Mac restart (session stays
# available in the mobile app without any terminal open). Guarded on credentials
# to avoid a crash loop when not logged in; if it exits (e.g. token expired), the
# container stays up idle so you can re-login and restart, rather than loop.
# Toggled by `make dev-remote-persist` / `make dev-remote-stop`.
if [ "${CLAUDE_REMOTE_PERSIST:-false}" = "true" ]; then
	if [ -f "$DEST/.credentials.json" ]; then
		echo "Starte persistente Remote Control fuer '${TICKET:-dev}' ..."
		claude --remote-control --name "${TICKET:-dev}" || echo "Remote Control beendet ($?); Container bleibt idle (make dev-login + Neustart)."
		exec sleep infinity
	else
		echo "CLAUDE_REMOTE_PERSIST=true, aber kein Login im Volume (make dev-login); bleibe idle."
	fi
fi

exec "$@"
