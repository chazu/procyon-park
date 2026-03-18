BIN = pp
GOBIN = $(shell go env GOPATH)/bin

build:
	mag build -o $(BIN)
	codesign -s - $(BIN)

full:
	mag build --full -o $(BIN)
	codesign -s - $(BIN)

install: build
	cp $(BIN) $(GOBIN)/$(BIN)
	codesign -f -s - $(GOBIN)/$(BIN)

run:
	mag -m Main.start

serve:
	./$(BIN) serve

clean:
	rm -f $(BIN) mag-custom *.image

.PHONY: build full install run serve clean
