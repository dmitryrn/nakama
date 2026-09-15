# Fork Workflow Guide

This document describes how to modify both `nakama` and `nakama-common` when adding new runtime functionality.

## Repositories

- **nakama-common fork**: `github.com/dmitryrn/nakama-common`
- **nakama fork**: `github.com/dmitryrn/nakama`
- **Docker Hub**: `dmitriirog/nakama` (note: NOT dmitryrn -- the Docker Hub account differs from the GitHub one)

## When You Need This

Use this workflow when you need to:
- Add new methods to `runtime.NakamaModule` interface
- Add new API endpoints (requires proto generation)
- Modify core server behavior that affects the runtime interface

## Protocol Buffers / gRPC Generation

**Not needed** if you're only adding pure runtime functions (like `PartyGet`) that:
- Don't expose new client-facing HTTP/gRPC endpoints
- Only return simple Go types (strings, slices, etc.)
- Are consumed by Go plugins via `runtime.NakamaModule`

**Needed** if you add:
- New REST API endpoints
- New realtime socket messages
- Changes to existing proto definitions

To generate protos when needed:
```bash
cd nakama-common
go install google.golang.org/protobuf/cmd/protoc-gen-go
env PATH="$HOME/go/bin:$PATH" go generate -x ./...
```

```bash
cd nakama
go install \
    google.golang.org/protobuf/cmd/protoc-gen-go \
    google.golang.org/grpc/cmd/protoc-gen-go-grpc \
    github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway \
    github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2
# install buf
./buf.sh
```

## Step-by-Step Workflow

### 1. Modify nakama-common (Runtime Interface)

Add your new method signature to the `NakamaModule` interface:

```go
// nakama-common/runtime/runtime.go
type NakamaModule interface {
    // ... existing methods ...
    PartyGet(ctx context.Context, partyID string) (leaderID string, memberUserIDs []string, err error)
}
```

**Commit and push:**
```bash
cd nakama-common
git add .
git commit -m "Add PartyGet runtime function"
git push origin forked
```

### 2. Modify nakama (Implementation)

Implement the logic in three places:

**A. Party Registry Interface** (`server/party_registry.go`):
```go
type PartyRegistry interface {
    // ... existing methods ...
    PartyGet(ctx context.Context, partyID string) (leaderID string, memberUserIDs []string, err error)
}
```

**B. Party Registry Implementation** (`server/party_registry.go`):
```go
func (p *LocalPartyRegistry) PartyGet(ctx context.Context, partyID string) (string, []string, error) {
    // Parse party ID, lookup party, return leader + members
}
```

**C. Go Runtime Module** (`server/runtime_go_nakama.go`):
```go
func (n *RuntimeGoNakamaModule) PartyGet(ctx context.Context, partyID string) (string, []string, error) {
    return n.partyRegistry.PartyGet(ctx, partyID)
}
```

### 3. Wire nakama to Your Forked nakama-common

Update `nakama/go.mod` to use your fork:

```go
replace github.com/heroiclabs/nakama-common => github.com/dmitryrn/nakama-common v1.45.1-forked
```

Then vendor dependencies:
```bash
cd nakama
export GOPRIVATE=github.com/dmitryrn/*  # Bypass Go module proxy for private/fresh tags
go mod tidy
go mod vendor
```

**Note**: If your fork is public but the tag is very fresh, Go's module proxy may not have cached it yet. `GOPRIVATE` tells Go to fetch directly from GitHub instead of the proxy.

### 4. Build and Verify nakama

```bash
cd nakama
go build -trimpath -mod=vendor
```

### 5. Push nakama Changes

```bash
cd nakama
git add .
git commit -m "Implement PartyGet runtime function"
git push origin forked
```

### 6. Build Custom Nakama Docker Image

The `nakama-pluginbuilder` image does **NOT** need to be rebuilt. It's just a Go toolchain container. Only the main `nakama` server image needs your changes.

```bash
cd nakama
docker build -f build/Dockerfile \
  --build-arg VERSION="3.38.1-forked" \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  -t dmitriirog/nakama:3.38.1-forked \
  -t dmitriirog/nakama:latest \
  .
```

Push to Docker Hub:
```bash
docker login
docker push dmitriirog/nakama:3.38.1-forked
docker push dmitriirog/nakama:latest
```

### 7. Update Your Plugin Project

In your plugin's `go.mod`, add the replace directive:

```go
replace github.com/heroiclabs/nakama-common => github.com/dmitryrn/nakama-common v1.45.1-forked
```

Update vendor:
```bash
cd your-plugin-project
export GOPRIVATE=github.com/dmitryrn/*
go mod tidy
go mod vendor
```

### 8. Update Your Plugin's Dockerfile

Use the custom nakama image while keeping the stock plugin builder:

```dockerfile
# ARG NAKAMA_VERSION=3.38.0
# ARG NAKAMA_IMAGE=heroiclabs/nakama
ARG NAKAMA_VERSION=3.38.1-forked
ARG NAKAMA_IMAGE=dmitriirog/nakama
ARG GO_BUILD_TAGS=

# Plugin builder stays stock - it's just a Go toolchain
FROM heroiclabs/nakama-pluginbuilder:3.38.0 AS builder
# ... build your plugin ...

# Use your custom nakama image (contains PartyGet implementation)
# FROM heroiclabs/nakama:${NAKAMA_VERSION}
FROM ${NAKAMA_IMAGE}:${NAKAMA_VERSION}
# ... copy plugin .so file ...
```

Build your plugin:
```bash
docker build -t your-plugin:latest .
```

## Summary of What Gets Modified

| Component | Needs Custom Build? | Notes |
|---|---|---|
| `nakama-common` | No (just push to GitHub) | Interface definitions only |
| `nakama` server | **Yes** | Build Docker image with your implementation |
| `nakama-pluginbuilder` | **No** | Stock image works fine - just Go toolchain |
| Your plugin | No | Just update `go.mod` replace directive |

## Important Notes

- **Always cut a NEW image tag when nakama-common changes.** A Go plugin and the nakama binary
  that loads it must be built against the exact same nakama-common version. Re-pushing an existing
  tag leaves stale copies cached on other machines, and the only symptom is the plugin refusing to
  load with a version-mismatch error that names no versions. Bump the tag and the two Dockerfiles
  in the plugin repo (`Dockerfile` and `test/Dockerfile`) together with the go.mod replace.
- **Go module proxy caching**: Fresh tags on GitHub take a few minutes to propagate through Go's module proxy (`proxy.golang.org`). Use `GOPRIVATE` to bypass this.
- **Vendor directory**: Committing `vendor/` ensures reproducible builds without network access.
- **Plugin builder image**: `heroiclabs/nakama-pluginbuilder` is just `golang` + `gcc`. It doesn't contain nakama-common, so it never needs rebuilding.
- **Runtime functions vs API**: If you're only adding Go runtime methods (consumed by plugins), no proto generation is needed. If you're adding HTTP/gRPC endpoints, you must regenerate protos.

## Troubleshooting

**Error: `unknown revision v1.45.1-forked`**
- Tag hasn't propagated through Go proxy yet
- Fix: `export GOPRIVATE=github.com/dmitryrn/*`

**Error: `404 Not Found` from sum.golang.org**
- Go proxy hasn't cached your module yet
- Fix: `export GOPRIVATE=github.com/dmitryrn/*`

**Error: Host key verification failed when pushing**
- Use SSH URL for remote: `git remote set-url origin git@github.com:dmitryrn/nakama.git`
- Or use HTTPS with token: `git remote set-url origin https://TOKEN@github.com/dmitryrn/nakama.git`
