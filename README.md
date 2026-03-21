# DNS to Route Resolver

A high-performance DNS log monitoring service written in Go that automatically discovers networks from DNS queries and manages BIRD routing configurations.

## Features

- **DNS Log Monitoring**: Monitors DNS query logs to discover networks
- **Automatic Route Management**: Adds discovered networks to BIRD configuration files
- **Clustering Support**: Uses memberlist for distributed coordination
- **Prometheus Metrics**: Built-in metrics endpoint for monitoring
- **Configurable**: YAML-based configuration
- **Atomic Operations**: Safe configuration updates with atomic renames

## How It Works

1. Monitors DNS logs for successful resolutions
2. Groups IP addresses into networks (configurable mask, default /24)
3. Maintains BIRD configuration files per group
4. Automatically reloads BIRD configuration when routes change
5. Supports clustering for multi-node deployments

## Requirements

- Go 1.21+
- BIRD Internet Routing Daemon
- Linux/macOS (tested on both platforms)

## Installation

### From Source

```bash
git clone https://github.com/Scorcher/dns-to-route-resolver.git
cd dns-to-route-resolver
make build
```

### From Releases

Download the latest binary from the [releases page](https://github.com/Scorcher/dns-to-route-resolver/releases).

## Configuration

Create a configuration file (e.g., `config.yaml`):

```yaml
# DNS to Route Resolver Configuration
bird:
  config_path_template: "/etc/bird/bird.conf.d/route-%s.conf"
  route_template: "route %s via \"wg0\";\n"
  reload_command: ["birdc", "configure"]

settings:
  network_mask: 24
  log_file_path: "/var/log/dns-to-route-resolver.log"
  flush_interval: "1m"
  resolved_timeout: "5m"

logging:
  level: "info"

metrics:
  enabled: true
  port: 9090
  path: "/metrics"

clustering:
  enabled: true
  port: 7946
  secret: "your-secret-key"
  advertise: "192.168.1.100:7946"
```

### Configuration Options

#### BIRD Configuration
- `config_path_template`: Template for BIRD config file paths (use `%s` for group name)
- `route_template`: Template for individual route entries
- `reload_command`: Command to reload BIRD configuration

#### Settings
- `network_mask`: CIDR mask for network aggregation (1-32)
- `log_file_path`: Path to DNS log file to monitor
- `flush_interval`: How often to flush buffered routes
- `resolved_timeout`: How long to keep resolved entries

#### Logging
- `level`: Log level (debug, info, warn, error)

#### Metrics
- `enabled`: Enable/disable Prometheus metrics
- `port`: Port for metrics server
- `path`: HTTP path for metrics endpoint

#### Clustering
- `enabled`: Enable/disable clustering
- `port`: Port for cluster communication
- `secret`: Shared secret for cluster authentication
- `advertise`: Address to advertise to other cluster members

## Usage

```bash
./dns-to-route-resolver config.yaml
```

## Building

```bash
# Build for current platform
make build

# Build for all supported platforms
make build-all

# Clean build artifacts
make clean

# Run tests
make test

# Run linter
make lint
```

## Docker

```bash
# Build Docker image
docker build -t dns-to-route-resolver .

# Run with config
docker run -v $(pwd)/config.yaml:/config.yaml dns-to-route-resolver /config.yaml
```

## Monitoring

The service exposes Prometheus metrics at `http://localhost:9090/metrics` when enabled:

- `dns_to_route_resolver_routes_total`: Total number of routes
- `dns_to_route_resolver_routes_added`: Routes added counter
- `dns_to_route_resolver_dns_queries_processed`: DNS queries processed

## Architecture

```
DNS Logs → Log Processor → Network Manager → BIRD Config Files
                              ↓
                         Clustering (optional)
                              ↓
                         Metrics (optional)
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Ensure CI passes
6. Submit a pull request

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Support

For issues and questions, please use the [GitHub issue tracker](https://github.com/Scorcher/dns-to-route-resolver/issues).
