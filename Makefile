.PHONY: build run test lint clean migrate dashboard build-go

BINARY=bin/assistant
GO=go

dashboard:
	cd dashboard && npm run build
	rm -rf cmd/assistant/dashboard_dist
	cp -r dashboard/dist cmd/assistant/dashboard_dist

build: dashboard
	$(GO) build -o $(BINARY) ./cmd/assistant

build-go:
	$(GO) build -o $(BINARY) ./cmd/assistant

run: build
	./$(BINARY) --config=configs/config.yaml

test:
	$(GO) test ./... -race -cover -count=1

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
deploy:
	GOOS=linux GOARCH=amd64 $(GO) build -o bin/assistant-linux ./cmd/assistant
	$(SSH) 'systemctl stop assistant'
	# Upload to a temp name then atomically rename: the running process holds the
	# inode open, so writing in place fails with ETXTBSY.
	$(SCP) bin/assistant-linux $(SERVER):/opt/assistant/assistant.new
	$(SCP) migrations/*.sql $(SERVER):/opt/assistant/migrations/
	$(SSH) 'mv -f /opt/assistant/assistant.new /opt/assistant/assistant && chmod +x /opt/assistant/assistant && systemctl start assistant'
	@echo "Deployed. Checking status..."
	@sleep 3
	@$(SSH) 'systemctl is-active assistant && journalctl -u assistant -n 5 --no-pager'

# Yuri is separate production. Deploy it only on purpose.
deploy-yuri:
	GOOS=linux GOARCH=amd64 $(GO) build -o bin/assistant-linux ./cmd/assistant
	$(SSH) 'systemctl stop assistant-yuri'
	$(SCP) bin/assistant-linux $(SERVER):/opt/assistant-yuri/assistant.new
	$(SSH) 'mv -f /opt/assistant-yuri/assistant.new /opt/assistant-yuri/assistant && chmod +x /opt/assistant-yuri/assistant && systemctl start assistant-yuri'
	@sleep 3
	@$(SSH) 'systemctl is-active assistant-yuri && journalctl -u assistant-yuri -n 5 --no-pager'
