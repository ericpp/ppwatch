# Run from a filesystem that supports file locking (see README "Building notes").

BINARY := ppwatch

all: test build

build:
	go build -o $(BINARY) ./cmd/ppwatch

test:
	go test -count=1 ./...

vet:
	go vet ./...

# Static Linux binary (works on any glibc/musl system).
linux:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY)-linux-amd64 ./cmd/ppwatch

docker:
	docker build --pull -t ericpp/ppwatch --load .

upload:
	docker push ericpp/ppwatch

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64
