.PHONY: build run test lint clean migrate dashboard build-go build-linux deploy deploy-yuri

BINARY=bin/assistant
BINARY_LINUX=bin/assistant-linux
GO=go
VERSION=$(shell git describe --always --dirty 2>/dev/null || echo dev)
LDFLAGS=-s -w -X main.version=$(VERSION)

dashboard:
	cd dashboard && npm run build
	rm -rf cmd/assistant/dashboard_dist
	cp -r dashboard/dist cmd/assistant/dashboard_dist

build: dashboard
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/assistant

build-go:
	@test -d cmd/assistant/dashboard_dist || $(MAKE) dashboard
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/assistant

build-linux: dashboard
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY_LINUX) ./cmd/assistant

run: build
	./$(BINARY) --config=configs/config.yaml

test:
	@test -d cmd/assistant/dashboard_dist || $(MAKE) dashboard
	$(GO) test ./... -race -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/ cmd/assistant/dashboard_dist

migrate:
	$(GO) run ./cmd/assistant --migrate --config=configs/config.yaml

# Moved to the Singapore box on 2026-09-17; the Newark IP no longer answers.
SERVER=root@139.162.18.24
SSH=ssh -i ~/.ssh/cryptoai_linode $(SERVER)
SCP=scp -i ~/.ssh/cryptoai_linode

# Each bot owns its binary now — yuri used to symlink to this one, so every
# deploy restarted a production instance that had asked for nothing.
# Uploads to .new first (avoids ETXTBSY), saves current working binary as .prev,
# swaps atomically, verifies health, and auto-rolls back .prev if startup fails.
deploy: build-linux
	$(SCP) $(BINARY_LINUX) $(SERVER):/opt/assistant/assistant.new
	$(SSH) 'set -e; \
		chmod 755 /opt/assistant/assistant.new; \
		[ -f /opt/assistant/assistant ] && cp -f /opt/assistant/assistant /opt/assistant/assistant.prev || true; \
		mv -f /opt/assistant/assistant.new /opt/assistant/assistant; \
		systemctl restart assistant; \
		sleep 3; \
		if ! systemctl is-active --quiet assistant; then \
			echo "ERROR: assistant failed to start, rolling back to assistant.prev"; \
			[ -f /opt/assistant/assistant.prev ] && cp -f /opt/assistant/assistant.prev /opt/assistant/assistant; \
			systemctl restart assistant; \
			journalctl -u assistant -n 20 --no-pager; \
			exit 1; \
		fi'
	@echo "Deployed $(VERSION). Checking status..."
	@$(SSH) '/opt/assistant/assistant --version && systemctl is-active assistant && journalctl -u assistant -n 5 --no-pager'

# Yuri is separate production (runs as user yuri). Deploy it only on purpose.
deploy-yuri: build-linux
	$(SCP) $(BINARY_LINUX) $(SERVER):/opt/assistant-yuri/assistant.new
	$(SSH) 'set -e; \
		chown yuri:yuri /opt/assistant-yuri/assistant.new && chmod 755 /opt/assistant-yuri/assistant.new; \
		[ -f /opt/assistant-yuri/assistant ] && cp -pf /opt/assistant-yuri/assistant /opt/assistant-yuri/assistant.prev || true; \
		mv -f /opt/assistant-yuri/assistant.new /opt/assistant-yuri/assistant; \
		systemctl restart assistant-yuri; \
		sleep 3; \
		if ! systemctl is-active --quiet assistant-yuri; then \
			echo "ERROR: assistant-yuri failed to start, rolling back to assistant.prev"; \
			[ -f /opt/assistant-yuri/assistant.prev ] && cp -pf /opt/assistant-yuri/assistant.prev /opt/assistant-yuri/assistant; \
			systemctl restart assistant-yuri; \
			journalctl -u assistant-yuri -n 20 --no-pager; \
			exit 1; \
		fi'
	@echo "Deployed $(VERSION) to yuri. Checking status..."
	@$(SSH) '/opt/assistant-yuri/assistant --version && systemctl is-active assistant-yuri && journalctl -u assistant-yuri -n 5 --no-pager'
