BINARY = terraform-provider-sentry
GO_LICENSES := go run github.com/google/go-licenses/v2@v2.0.1

.PHONY: build install vet fmt fmt-check lint test test-terraform test-tofu testacc tidy generate licenses license-check vulncheck

build: ## Compile the provider binary
	go build -trimpath -o bin/$(BINARY) .

install: ## Install the provider into GOBIN
	go install .

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	test -z "$$(gofmt -l .)"

lint:
	golangci-lint run

test: ## Unit and protocol tests against an in-memory Sentry API (no network, needs terraform on PATH)
	go test -race -count=1 -cover ./...

test-terraform: ## Protocol tests through Terraform
	./scripts/test-protocol.sh terraform

test-tofu: ## Protocol tests through OpenTofu
	./scripts/test-protocol.sh tofu

# Acceptance tests hit the real Sentry API. Required env:
#   SENTRY_AUTH_TOKEN, SENTRY_TEST_ORG (slug of an organization the tests may write to;
#   they create and delete tf-acc-* teams and projects)
# Optional: SENTRY_TEST_MEMBER_ID (an existing member, for team membership) and
#   SENTRY_TEST_INVITE_EMAIL (an address that is not a member; Sentry emails it)
#   SENTRY_TEST_ORG_SETTINGS=1 (rewrites the organization's data scrubbing settings unchanged)
testacc:
	TF_ACC=1 go test -count=1 -timeout 30m -v -run '^TestAcc' ./internal/provider/

tidy:
	go mod tidy

generate: ## Regenerate docs/ from schema + examples + templates
	go generate ./...

licenses: ## Collect dependency licenses and notices for release archives
	$(GO_LICENSES) save ./... --save_path=third_party --force --ignore=github.com/solcreek/terraform-provider-sentry

license-check:
	$(GO_LICENSES) check --include_tests ./... --allowed_licenses=MPL-2.0,Apache-2.0,BSD-2-Clause,BSD-3-Clause,MIT,ISC,Unicode-DFS-2016

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
