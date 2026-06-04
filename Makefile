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

SERVER=root@194.195.252.68
SSH=ssh -i ~/.ssh/cryptoai_linode $(SERVER)
SCP=scp -i ~/.ssh/cryptoai_linode

deploy:
	GOOS=linux GOARCH=amd64 $(GO) build -o bin/assistant-linux ./cmd/assistant
	$(SSH) 'systemctl stop assistant assistant-yuri'
	# Upload to a temp name then atomically rename. Both bots share this binary
	# (yuri symlinks to it) and hold the inode open, so writing in place fails (ETXTBSY).
	$(SCP) bin/assistant-linux $(SERVER):/opt/assistant/assistant.new
	$(SCP) migrations/*.sql $(SERVER):/opt/assistant/migrations/
	$(SSH) 'mv -f /opt/assistant/assistant.new /opt/assistant/assistant && chmod +x /opt/assistant/assistant && systemctl start assistant assistant-yuri'
	@echo "Deployed. Checking status..."
	@sleep 3
	@$(SSH) 'systemctl is-active assistant assistant-yuri && journalctl -u assistant-yuri -n 5 --no-pager'
