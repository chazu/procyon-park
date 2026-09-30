BIN = pp
GOBIN = $(shell go env GOPATH)/bin
# pp does not yet run on current maggie HEAD (GansoDatabase primOpen: and
# trimBoth were renamed upstream). Build with a mag pinned to maggie 46de074
# when one is installed as mag-pp; override with `make MAG=mag` once ported.
MAG ?= $(or $(shell command -v mag-pp 2>/dev/null),mag)

build:
	rm -f $(BIN)
	$(MAG) build -o $(BIN)
	codesign -s - $(BIN)

full:
	rm -f $(BIN)
	$(MAG) build --full -o $(BIN)
	codesign -s - $(BIN)

install: build
	cp $(BIN) $(GOBIN)/$(BIN)
	codesign -f -s - $(GOBIN)/$(BIN)
	mkdir -p $(HOME)/.pp/static
	cp -r static/. $(HOME)/.pp/static/

run:
	$(MAG) -m Main.start

serve:
	./$(BIN) serve

clean:
	rm -f $(BIN) mag-custom *.image

.PHONY: build full install run serve clean
