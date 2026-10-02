# Build and install the doubao CPA plugin.
#
#   make            build the plugin
#   make test       run offline unit tests
#   make test-live  run tests against the real upstream (needs credentials)
#   make install    copy into a CPA deployment (CPA_DIR=...)
#   make clean      remove build artifacts

PLUGIN      := doubao
GO          ?= go
CPA_DIR     ?= ../CLIProxyAPI

.PHONY: all build test test-live race vet fmt install clean

all: build

# The plugin is a c-shared library, so CGO is mandatory. CPA itself must also be
# built with CGO_ENABLED=1 or it cannot load any plugin.
build:
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -o $(PLUGIN).so .
	@# The build emits a header next to the library; CPA never reads it and it
	@# only confuses anyone browsing the plugins directory.
	@rm -f $(PLUGIN).h
	@ls -lh $(PLUGIN).so

# Offline: no account, no network. This is what CI should run.
test:
	CGO_ENABLED=0 $(GO) test -count=1 ./...

# Live tests talk to the real upstream and need a credential:
#   export DOUBAO_LIVE_COOKIES='sessionid=...; flow_cur_user_sec_id=...'
#   export DOUBAO_LIVE_REALM=doubao        # or dola
# Image generation consumes quota and needs DOUBAO_LIVE_IMAGE=1.
test-live:
	CGO_ENABLED=0 $(GO) test -count=1 -v -timeout 300s \
		-run 'TestLiveLaunch|TestLiveChatRoundTrip' ./...

test-image:
	DOUBAO_LIVE_IMAGE=1 CGO_ENABLED=0 $(GO) test -count=1 -v -timeout 600s \
		-run TestLiveImageGeneration ./...

race:
	CGO_ENABLED=1 $(GO) test -race -count=1 ./...

vet:
	CGO_ENABLED=0 $(GO) vet ./...

fmt:
	$(GO) fmt ./...

install: build
	@test -d "$(CPA_DIR)" || { echo "CPA 目录不存在: $(CPA_DIR)"; exit 1; }
	@mkdir -p "$(CPA_DIR)/plugins"
	cp $(PLUGIN).so "$(CPA_DIR)/plugins/"
	@echo "已安装到 $(CPA_DIR)/plugins/$(PLUGIN).so"
	@echo "请在 $(CPA_DIR)/config.yaml 中确认："
	@echo
	@echo "  plugins:"
	@echo "    enabled: true"
	@echo "    dir: \"plugins\""
	@echo "    configs:"
	@echo "      $(PLUGIN):"
	@echo "        enabled: true"
	@echo "        realm_default: doubao"

clean:
	rm -f $(PLUGIN).so $(PLUGIN).h
