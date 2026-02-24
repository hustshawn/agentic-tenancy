# CLI Migration Guide: Bash → Go

This guide helps migrate from the legacy bash CLI (`scripts/ztm.sh`) to the new Go binary (`ztm`).

## Installation

**Old (bash):**
```bash
# Add to PATH
export PATH="$PATH:/path/to/agentic-tenancy/scripts"
ztm.sh tenant list
```

**New (Go):**
```bash
# Build and install
make install-ztm

# Use directly
ztm tenant list
```

## Command Changes

All commands are **identical** except for the binary name:

| Old | New |
|-----|-----|
| `ztm.sh tenant create ...` | `ztm tenant create ...` |
| `ztm.sh tenant list` | `ztm tenant list` |
| `ztm.sh tenant get <id>` | `ztm tenant get <id>` |
| `ztm.sh tenant update ...` | `ztm tenant update ...` |
| `ztm.sh tenant delete <id>` | `ztm tenant delete <id>` |
| `ztm.sh webhook register <id>` | `ztm webhook register <id>` |

## Environment Variables

Same variables, same behavior:

- `ZTM_NAMESPACE` (default: tenants)
- `ZTM_KUBE_CONTEXT` (default: current context)
- `ZTM_ORCHESTRATOR_URL` (default: use kubectl exec)
- `ZTM_ROUTER_URL` (default: use kubectl exec)

## Flags

Same flags, same behavior:

- `--namespace`
- `--context`
- `--orchestrator-url`
- `--router-url`
- `--idle-timeout` (tenant create/update)
- `--bot-token` (tenant update)

## New Features in Go CLI

- `--output json` - JSON output for all commands
- `--no-color` - Disable colored output
- `ztm version` - Show version info
- Better error messages
- Shell completions (coming in v0.2.0)
- No python3 dependency for JSON formatting

## Removed Dependencies

**Old requirements:**
- bash
- kubectl
- curl
- python3 (for JSON formatting)

**New requirements:**
- kubectl only (no python3, no curl)

## Deprecation Timeline

| Version | Status |
|---------|--------|
| v0.1.0  | Both CLIs available, Go recommended |
| v0.5.0  | Bash CLI prints deprecation warning |
| v1.0.0  | Bash CLI removed |

## Migration Checklist

- [ ] Install Go CLI: `make install-ztm`
- [ ] Test commands: `ztm tenant list`
- [ ] Update scripts/CI to use `ztm` instead of `ztm.sh`
- [ ] Update documentation/runbooks
- [ ] Remove `scripts/ztm.sh` from PATH
