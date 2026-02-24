# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.0] - 2026-02-23

### Added

- Go-based CLI using Cobra framework
- All commands from bash CLI: tenant (create/list/get/update/delete), webhook (register)
- kubectl exec-based API client (no direct HTTP dependencies)
- Colored terminal output with --no-color flag
- JSON output format (--output json)
- Global flags: --namespace, --context, --orchestrator-url, --router-url
- Version command with build info
- Cross-platform support (darwin/linux, amd64/arm64)
- Comprehensive test coverage (unit + integration)
- Documentation updates and migration guide

### Changed

- Bash CLI (`scripts/ztm.sh`) marked as deprecated
- README updated with Go CLI installation instructions

### Deprecated

- `scripts/ztm.sh` will be removed in v1.0.0

## [Unreleased]

### Planned for v0.2.0

- `ztm logs` command (stream logs from orchestrator/router/tenant pods)
- Shell completions (bash/zsh/fish)

### Planned for v0.3.0

- `ztm status` command (health dashboard, warm pool metrics)

### Planned for v1.0.0

- Remove bash CLI
- Mark as production-ready
