# Memory Test Project

This project demonstrates Docker memory limits by creating a Go HTTP server that intentionally consumes memory until it hits the container's memory limit.

## Setup

### 1. Start the container
```bash
docker-compose up --build
```

### 2. Test memory overload
```bash
# Run automated stress test
./stress-test.sh

# Or manually allocate memory
./stress-test.sh allocate 50   # Allocate 50MB
./stress-test.sh status        # Check memory status
```

## How it works

- **Container limit**: 128MB (set in docker-compose.yml)
- **Server endpoints**:
  - `GET /` - Health check
  - `POST /allocate?mb=N` - Allocate N MB of memory
  - `GET /status` - Show memory statistics
- **Expected behavior**: Container should crash when memory usage exceeds 128MB

## Memory Limit Configuration

The memory limit is set in `docker-compose.yml`:
```yaml
deploy:
  resources:
    limits:
      memory: 128M    # Hard limit
    reservations:
      memory: 64M     # Soft reservation
```

## Observing the Crash

Watch the container logs:
```bash
docker-compose logs -f memory-test
```

The container will be killed by the OOM killer when it exceeds the memory limit.