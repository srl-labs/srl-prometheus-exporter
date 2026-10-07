# VERSION is the release version used by goreleaser when building.
# Update this file when cutting a new release so local builds use the correct version.
VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")

.PHONY: help release release-snapshot dev
help:
	@echo "Targets:"
	@echo "  release          Build release packages with goreleaser (uses VERSION file)"
	@echo "  release-snapshot Build snapshot packages from the current tree (VERSION file)"
	@echo "  dev              Same as release-snapshot (deletes dist first)"

release:
	GORELEASER_CURRENT_TAG=v$(VERSION) goreleaser release --clean -f .goreleaser.yml

release-snapshot:
	rm -rf dist
	GORELEASER_CURRENT_TAG=v$(VERSION) goreleaser release --snapshot --clean -f .goreleaser.yml

dev: release-snapshot
