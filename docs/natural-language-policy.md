# Natural Language Policy Engine

VaultRun's Natural Language Policy Engine vertaalt gewone taal naar executable security policies. Schrijf "Allow github.com and pypi.org, max 2 CPU" en krijg automatisch OPA policies, Docker constraints, en network rules.

## 🎯 Quick Start

### 1. Basis Gebruik

```bash
# Parse natuurlijke taal naar structured policy
curl -X POST http://localhost:8080/api/v1/policies/parse \
  -H "Authorization: Bearer vr_your_key" \
  -H "Content-Type: application/json" \
  -d '{
    "natural_language": "Python data science environment. Allow pypi.org and github.com for packages. Max 2 CPU cores, 4GB RAM, 30 minute timeout. Block sudo and network tools like curl/wget."
  }'
```

**Response:**
```json
{
  "resource_limits": {
    "cpu_limit": 2.0,
    "memory_limit_mb": 4096,
    "timeout_seconds": 1800,
    "max_processes": 100
  },
  "network_policy": {
    "enabled": true,
    "allowed_hosts": ["pypi.org", "github.com"],
    "blocked_ports": [],
    "allow_dns": true,
    "allow_loopback": false
  },
  "command_policy": {
    "allowed_commands": ["python", "pip", "git"],
    "blocked_commands": ["curl", "wget", "nc"],
    "block_sudo": true,
    "block_network": false
  },
  "file_policy": {
    "allowed_paths": ["/workspace"],
    "blocked_paths": ["/etc", "/root", "/proc"],
    "read_only": false
  },
  "explanation": "Python data science environment with package access. Restricted network, no sudo, 2 CPU/4GB/30min limits.",
  "warnings": ["Blocking curl/wget may affect some workflows"]
}
```

### 2. Compile naar Executable Formats

```bash
# Parse + compile in één keer
curl -X POST http://localhost:8080/api/v1/policies/compile \
  -H "Authorization: Bearer vr_your_key" \
  -H "Content-Type: application/json" \
  -d '{
    "natural_language": "Allow github.com only. Block all sudo. 1 CPU, 512MB RAM."
  }'
```

**Response:**
```json
{
  "source_policy": { ... },
  "opa_policy": "package vaultrun\n\ndefault allow = false\n\n...",
  "docker_config": {
    "cpu_limit": 1.0,
    "memory_limit_mb": 512,
    "network_mode": "bridge",
    "cap_drop": ["ALL"],
    "read_only_rootfs": false
  },
  "network_rules": {
    "enabled": true,
    "allowed_hosts": ["github.com"],
    "iptables_rules": [
      "iptables -P OUTPUT DROP",
      "iptables -A OUTPUT -p udp --dport 53 -j ACCEPT",
      "iptables -A OUTPUT -d github.com -j ACCEPT"
    ]
  }
}
```

### 3. Policy Templates

```bash
# List available templates
curl http://localhost:8080/api/v1/policies/templates \
  -H "Authorization: Bearer vr_your_key"

# Get specific template
curl http://localhost:8080/api/v1/policies/templates/python-data-science \
  -H "Authorization: Bearer vr_your_key"

# Generate policy from template
curl -X POST http://localhost:8080/api/v1/policies/from-template/python-data-science \
  -H "Authorization: Bearer vr_your_key"
```

## 📚 Available Templates

### 1. Python Data Science
```
Category: data-science
Use case: Python environment voor data analysis

Features:
- Network: pypi.org, github.com
- Resources: 2 CPU, 4GB RAM, 30min timeout
- Commands: python, pip, git, jupyter
- Blocks: curl, wget, sudo
```

### 2. Node.js Web App
```
Category: web-dev
Use case: Node.js development environment

Features:
- Network: registry.npmjs.org, github.com, ports 3000-9000
- Resources: 2 CPU, 2GB RAM, 60min timeout
- Commands: node, npm, npx, git
- Blocks: curl, wget, sudo
```

### 3. Security Audit
```
Category: security
Use case: Air-gapped audit environment

Features:
- Network: Completely disabled
- Resources: 1 CPU, 1GB RAM, 15min timeout
- Commands: Only ls, cat, grep, find, sha256sum
- Read-only filesystem
```

### 4. Unrestricted Dev
```
Category: development
Use case: Trusted development workflows

Features:
- Network: All domains, all ports
- Resources: 4 CPU, 8GB RAM, 120min timeout
- Commands: All except sudo
- Full /workspace access
```

## 🔧 Configuration

### OpenAI API Key

Set `OPENAI_API_KEY` environment variable voor LLM-based parsing:

```bash
export OPENAI_API_KEY=sk-...
```

Zonder API key gebruikt VaultRun een mock parser met heuristic-based parsing (basic maar werkt).

### Model Selection

Default model is GPT-4. Andere modellen:
- `gpt-4-turbo` - Sneller, goedkoper
- `gpt-3.5-turbo` - Budget optie

## 💡 Example Policies

### Web Scraping Bot
```
Natural Language:
"Web scraping bot. Allow HTTP/HTTPS to any domain. Block SSH, FTP, SMTP ports. 
Max 1 CPU, 1GB RAM, 10 minute timeout. Allow python, requests library. 
Block subprocess, os.system calls."
```

### CI/CD Pipeline
```
Natural Language:
"CI/CD environment. Allow github.com for repo access, docker.io for images. 
Max 4 CPU, 8GB RAM, 60 minute timeout. Allow git, docker, npm, pip. 
Block network tools except curl for health checks."
```

### Database Admin
```
Natural Language:
"Database admin console. Allow connection to postgres.example.com only. 
Block all other network. Max 2 CPU, 4GB RAM, 30 minute timeout. 
Allow psql, pg_dump. Block DDL statements (DROP, TRUNCATE)."
```

### Student Sandbox
```
Natural Language:
"Student coding environment. No network access at all. Max 1 CPU, 512MB RAM, 
5 minute timeout. Allow python, gcc basics. Block file writes outside /workspace. 
Read-only system directories."
```

## 🛡️ Security Best Practices

### 1. Principle of Least Privilege
```
Bad:  "Allow all network, max 8 CPU, unlimited timeout"
Good: "Allow pypi.org only, max 1 CPU, 10 minute timeout"
```

### 2. Explicit Allowlists
```
Bad:  "Block curl and wget"  (still allows other network tools)
Good: "Allow only python and pip"  (explicit allowlist)
```

### 3. Block Sudo by Default
```
Always include: "No sudo or privileged operations"
```

### 4. Time Limits
```
Short jobs:   "5 minute timeout"
Medium jobs:  "30 minute timeout"
Long jobs:    "120 minute timeout"
```

### 5. Resource Limits
```
Lightweight: "1 CPU, 512MB RAM"
Standard:    "2 CPU, 2GB RAM"
Heavy:       "4 CPU, 8GB RAM"
```

## 🔍 Policy Validation

```bash
# Validate before applying
curl -X POST http://localhost:8080/api/v1/policies/validate \
  -H "Authorization: Bearer vr_your_key" \
  -H "Content-Type: application/json" \
  -d '{
    "natural_language": "Your policy here..."
  }'
```

**Response:**
```json
{
  "valid": true,
  "errors": [],
  "warnings": [
    "CPU limit > 8 cores may be excessive",
    "Network enabled with no host restrictions"
  ],
  "suggestions": [
    "Consider adding specific allowed_hosts",
    "Add timeout to prevent runaway processes"
  ]
}
```

## 🧪 Testing Policies

### 1. Dry Run
Parse en compile zonder creating session:
```bash
curl -X POST http://localhost:8080/api/v1/policies/compile \
  -H "Authorization: Bearer vr_your_key" \
  -d '{"natural_language": "..."}'
```

### 2. Inspect Generated OPA
Check de OPA Rego code:
```bash
curl -X POST http://localhost:8080/api/v1/policies/compile \
  -H "Authorization: Bearer vr_your_key" \
  -d '{"natural_language": "..."}' | jq -r '.opa_policy'
```

### 3. Test in Session
Create session met policy en test:
```bash
# Create session (hypothetical endpoint extension)
curl -X POST http://localhost:8080/api/v1/sessions \
  -H "Authorization: Bearer vr_your_key" \
  -d '{
    "image": "python:3.12-slim",
    "policy_natural_language": "Allow pypi.org only, max 1 CPU"
  }'
```

## 🚨 Troubleshooting

### "Failed to parse policy"
- Check OPENAI_API_KEY is set
- Verify OpenAI API access
- Fallback: mock parser gebruikt heuristics

### "Policy too permissive"
- Add explicit allowlists
- Reduce resource limits
- Enable more blocks

### "Policy too restrictive"
- Check warnings in validation response
- Add necessary allowed_hosts
- Increase resource limits

### "OPA policy rejected command"
- Check allowed_commands list
- Verify command isn't in blocked_commands
- Review OPA policy output

## 📊 Monitoring

### Policy Usage Metrics
```sql
-- Track most used templates
SELECT template_name, COUNT(*) 
FROM policy_usage 
GROUP BY template_name 
ORDER BY COUNT(*) DESC;
```

### Policy Violations
```sql
-- Track policy rejections
SELECT policy_id, violation_type, COUNT(*)
FROM policy_violations
GROUP BY policy_id, violation_type;
```

## 🔗 Integration

### With Session Creation
```go
import "github.com/nickvd7/vaultrun/internal/nlpolicy"

parser := nlpolicy.NewOpenAIParser(apiKey)
compiler := nlpolicy.NewCompiler()

// Parse NL policy
policy, _ := parser.Parse(ctx, "Allow github.com, max 2 CPU")

// Compile
compiled, _ := compiler.CompileAll(policy)

// Create session with compiled policy
session := CreateSession(SessionOpts{
    Image:         "python:3.12-slim",
    CPULimit:      compiled.DockerConfig.CPULimit,
    MemoryLimitMB: compiled.DockerConfig.MemoryLimitMB,
    OPAPolicy:     compiled.OPAPolicy,
    NetworkMode:   compiled.DockerConfig.NetworkMode,
})
```

### With CLI
```bash
# Future CLI command
vaultrun policy parse "Allow github.com, 2 CPU, 4GB RAM"
vaultrun policy validate -f my-policy.txt
vaultrun session create --policy-nl "Allow pypi.org only"
```

## 📈 Future Enhancements

- [ ] Custom policy templates
- [ ] Policy history/versioning
- [ ] Policy diff/comparison
- [ ] Visual policy builder
- [ ] Policy recommendation engine
- [ ] Multi-language support (currently English only)

## 🤝 Contributing

Nieuwe templates toevoegen in `internal/nlpolicy/types.go`:

```go
{
    Name:        "my-template",
    Description: "Description here",
    Category:    "category",
    Template:    `Natural language template here...`,
}
```

## 📚 Resources

- [OPA Policy Language (Rego)](https://www.openpolicyagent.org/docs/latest/policy-language/)
- [Docker Resource Constraints](https://docs.docker.com/config/containers/resource_constraints/)
- [OpenAI API Documentation](https://platform.openai.com/docs/api-reference)
