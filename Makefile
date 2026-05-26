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

deploy:
	GOOS=linux GOARCH=amd64 $(GO) build -o bin/assistant-linux ./cmd/assistant
	ssh -i ~/.ssh/cryptoai_linode root@172.104.56.5 'systemctl stop assistant'
	scp -i ~/.ssh/cryptoai_linode bin/assistant-linux root@172.104.56.5:/opt/assistant/assistant
	scp -i ~/.ssh/cryptoai_linode migrations/*.sql root@172.104.56.5:/opt/assistant/migrations/
	ssh -i ~/.ssh/cryptoai_linode root@172.104.56.5 'chmod +x /opt/assistant/assistant && systemctl start assistant'
	@echo "Deployed. Checking status..."
	@sleep 3
	@ssh -i ~/.ssh/cryptoai_linode root@172.104.56.5 'systemctl is-active assistant && journalctl -u assistant -n 5 --no-pager'
