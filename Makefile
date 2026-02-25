.PHONY: all image pp install test clean

# Build the pp binary
all: pp

# Compile Maggie sources into an image
image:
	mag --save-image procyon-park.image src/... lib/...

# Copy image and build the Go binary
pp: image
	cp procyon-park.image cmd/pp/
	go build -o pp ./cmd/pp/

# Codesign and install to ~/go/bin
install: pp
	codesign -s - pp
	cp pp ~/go/bin/pp

# Run tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -f pp procyon-park.image cmd/pp/procyon-park.image
