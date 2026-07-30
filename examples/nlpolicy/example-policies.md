# Natural Language Policy Examples

Praktische voorbeelden van VaultRun policies in natuurlijke taal.

## Data Science & ML

### Python Data Analysis
```
Python data analysis environment with pandas and numpy.

Network access:
- Allow pypi.org for installing packages
- Allow github.com for cloning repositories
- Block all other connections

Resources:
- Maximum 2 CPU cores
- Maximum 4GB memory  
- 30 minute timeout

Commands:
- Allow python, pip, git, jupyter
- Block curl, wget, netcat
- No sudo or privileged operations

Files:
- Full access to /workspace
- No access to /etc, /root, /proc
```

### TensorFlow Training
```
Machine learning training environment.

Network:
- Allow pypi.org, tensorflow.org, huggingface.co
- Allow github.com

Resources:
- Maximum 4 CPU cores
- Maximum 8GB memory
- 120 minute timeout
- GPU access if available

Commands:
- Allow python, pip, git
- Block network tools

Files:
- Read/write /workspace
- No system directory access
```

## Web Development

### Node.js API Development
```
Node.js REST API development environment.

Network:
- Allow registry.npmjs.org
- Allow github.com
- Allow ports 3000-9000 for dev servers
- Block production ports (80, 443, 8080)

Resources:
- 2 CPU cores
- 2GB memory
- 60 minute timeout

Commands:
- Allow node, npm, npx, git
- Block sudo, curl (use npm for dependencies)

Files:
- Full access to /workspace
```

### Frontend Build
```
Frontend build environment (React/Vue/Angular).

Network:
- Allow registry.npmjs.org
- Allow cdn.jsdelivr.net, unpkg.com
- Block all others

Resources:
- 4 CPU cores (for fast builds)
- 4GB memory
- 30 minute timeout

Commands:
- Allow node, npm, npx, git
- No network tools
- No sudo

Files:
- Read/write /workspace
```

## Security & Compliance

### Security Audit Sandbox
```
Air-gapped environment for security auditing.

Network:
- Completely disabled
- No external connections allowed

Resources:
- 1 CPU core
- 1GB memory
- 15 minute timeout

Commands:
- Allow ONLY: ls, cat, grep, find, sha256sum, diff
- Block everything else including compilers
- No sudo

Files:
- Read-only access to /workspace
- No write operations anywhere
```

### Malware Analysis
```
Isolated malware analysis environment.

Network:
- Completely disabled (air-gapped)
- No DNS, no loopback

Resources:
- 1 CPU core
- 512MB memory
- 10 minute timeout

Commands:
- Allow: strings, file, hexdump, sha256sum
- Block: all execution, compilation, networking
- No sudo

Files:
- Read-only /workspace
- No system access
```

## Database Operations

### PostgreSQL Admin
```
PostgreSQL database administration.

Network:
- Allow postgres.example.com:5432 ONLY
- Block all other network
- No external internet

Resources:
- 2 CPU cores
- 2GB memory
- 30 minute timeout

Commands:
- Allow: psql, pg_dump, pg_restore
- Block: all other commands
- No sudo

Files:
- Read/write /workspace for dumps
```

### MongoDB Operations
```
MongoDB operations console.

Network:
- Allow mongodb.example.com:27017 ONLY

Resources:
- 2 CPU cores
- 4GB memory
- 45 minute timeout

Commands:
- Allow: mongosh, mongodump, mongoexport
- Block network tools
- No sudo
```

## CI/CD & Automation

### GitHub Actions Runner
```
CI/CD pipeline environment.

Network:
- Allow github.com
- Allow docker.io, ghcr.io (container registries)
- Allow pypi.org, registry.npmjs.org
- Block all others

Resources:
- 4 CPU cores
- 8GB memory
- 60 minute timeout

Commands:
- Allow: git, docker, npm, pip, make
- Allow curl for health checks
- No sudo

Files:
- Full /workspace access
```

### Test Runner
```
Automated test execution environment.

Network:
- Disabled (tests should be self-contained)

Resources:
- 2 CPU cores
- 4GB memory
- 30 minute timeout

Commands:
- Allow: pytest, jest, go test, cargo test
- Block network tools
- No sudo

Files:
- Read-only /workspace (tests shouldn't modify code)
```

## Educational

### Student Coding Environment
```
Safe environment for student code execution.

Network:
- Completely disabled
- No external access

Resources:
- 1 CPU core maximum
- 512MB memory maximum
- 5 minute timeout (prevent runaway code)

Commands:
- Allow: python, gcc, g++ (basic compilers)
- Block: all network tools, system tools
- No sudo

Files:
- Read/write ONLY /workspace
- No system directory access
- Size limit: 100MB total
```

### Coding Interview Sandbox
```
Live coding interview environment.

Network:
- Disabled

Resources:
- 2 CPU cores
- 2GB memory
- 60 minute timeout

Commands:
- Allow: python, node, java, go, rust
- Allow: git (for submission)
- Block: network tools, sudo

Files:
- Full /workspace access
- No system access
```

## Specialized

### PDF Processing
```
PDF manipulation and processing.

Network:
- Disabled (work on local files only)

Resources:
- 2 CPU cores
- 4GB memory (large PDFs)
- 30 minute timeout

Commands:
- Allow: python, pdftk, gs (ghostscript)
- Block: network tools, sudo

Files:
- Read/write /workspace
```

### Image Processing
```
Image manipulation environment.

Network:
- Allow github.com for library updates
- Block all else

Resources:
- 4 CPU cores (parallel processing)
- 8GB memory (large images)
- 60 minute timeout

Commands:
- Allow: python, imagemagick, ffmpeg
- Block: network tools except pip/git

Files:
- Full /workspace access
- Size limit: 10GB
```

## Minimal Policies

### Ultra Restricted
```
Minimum possible privileges.

Network: Disabled
Resources: 1 CPU, 256MB, 5min
Commands: ls, cat only
Files: Read-only /workspace
```

### Read-Only Viewer
```
File viewing only.

Network: Disabled
Resources: 1 CPU, 512MB, 10min
Commands: ls, cat, grep, less, head, tail
Files: Read-only /workspace
```

### Compute Only
```
Pure computation, no I/O.

Network: Disabled
Resources: 8 CPU, 16GB, 120min
Commands: python, numpy/scipy only
Files: Read-only /workspace, write to /tmp only
```

## Usage Tips

### Combining Policies
```
Base policy: "Python environment"
+ "Allow github.com"
+ "Max 2 CPU, 4GB RAM"
+ "Block sudo and network tools"
```

### Incremental Tightening
```
Start: "Development environment with network"
Test → "Development environment, allow github.com only"
Test → "Development environment, allow github.com, block sudo"
Test → "Development environment, allow github.com, block sudo, 2 CPU max"
```

### Template Customization
```
Start with template: "python-data-science"
Modify: "Same as python-data-science but also allow huggingface.co"
```
