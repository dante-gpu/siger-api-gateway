# Siger API Gateway

DanteGPU's high-performance, cloud-native API Gateway for the NVIDIA GPU-based AI training platform. The gateway provides a unified entry point for REST APIs, gRPC services, and asynchronous job processing.

## Features

- REST API routing and handling
- Service discovery using Consul
- Asynchronous job processing with NATS JetStream
- Load balancing of backend services
- Metrics collection with Prometheus
- Structured logging with Zap
- Graceful shutdown handling
- Health checking

## Prerequisites

- Go 1.24+
- Docker (for containerization)
- Consul (for service discovery)
- NATS (for message queuing)

## Getting Started

### Installation

Clone the repository:

```bash
git clone https://github.com/yourusername/siger-api-gateway.git
cd siger-api-gateway
```

Build the application:

```bash
go build -o gateway cmd/main.go
```

### Configuration

Configuration is handled through a YAML file located at `configs/config.yaml`. The application will create a default configuration if none exists.

```yaml
port: ":8080"
consul_address: "localhost:8500"
nats_address: "localhost:4222"
log_level: "info"
```

### Running Locally

```bash
./gateway
```

### Running with Docker

Build the Docker image:

```bash
docker build -t siger-api-gateway .
```

Run the container:

```bash
docker run -p 8080:8080 siger-api-gateway
```

## API Endpoints

### Health Check

```
GET /health
```

Returns a 200 OK response if the service is healthy.

### Metrics

```
GET /metrics
```

Returns Prometheus metrics for monitoring.

### Job Submission

```
POST /api/v1/jobs
```

Submit a new job to be processed asynchronously.

Request body:

```json
{
  "type": "ai_training",
  "name": "BERT Fine-tuning",
  "description": "Fine-tune BERT model on custom dataset",
  "gpu_type": "A100",
  "gpu_count": 4,
  "priority": 10,
  "params": {
    "model": "bert-base-uncased",
    "dataset_path": "s3://mybucket/datasets/custom-data",
    "epochs": 3,
    "batch_size": 32,
    "learning_rate": 5e-5
  },
  "tags": ["nlp", "bert", "fine-tuning"]
}
```

Response:

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "queued",
  "timestamp": "2023-08-15T12:34:56Z",
  "message": "Job submitted successfully"
}
```

### Job Status

```
GET /api/v1/jobs/{jobID}
```

Get the status of a job.

Response:

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "processing",
  "timestamp": "2023-08-15T12:35:30Z",
  "message": "Job is currently processing"
}
```

### Cancel Job

```
DELETE /api/v1/jobs/{jobID}
```

Cancel a running job.

Response:

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "cancelling",
  "timestamp": "2023-08-15T12:40:00Z",
  "message": "Job cancellation requested"
}
```

## Architecture

The Siger API Gateway serves as the entry point for all client requests, routing them to the appropriate backend services or processing them asynchronously through NATS.

### Components

- **Router**: Uses the Chi router for HTTP request handling.
- **Service Discovery**: Integrates with Consul to discover backend services.
- **Load Balancer**: Distributes requests among healthy backend instances.
- **Message Queue**: Uses NATS for asynchronous job processing.
- **Metrics**: Collects Prometheus metrics for monitoring.
- **Logging**: Uses structured logging with Zap.

### Flow

1. Client sends a request to the API Gateway.
2. The request goes through middleware for authentication, metrics, etc.
3. The router determines where to send the request:
   - For synchronous APIs, it's forwarded to a backend service.
   - For asynchronous jobs, it's published to NATS.
4. For service discovery, the gateway queries Consul for healthy instances.
5. Load balancing selects the appropriate instance to handle the request.
6. The response is returned to the client.

## Development

### Adding a New API Endpoint

1. Create a new handler in the `internal/handlers` directory.
2. Register the handler with the router in `cmd/main.go`.

### Adding a New Service

1. Implement service discovery in the new service.
2. Register the service with Consul on startup.
3. Update the API Gateway to route requests to the new service.

## License

This project is licensed under the MIT License - see the LICENSE file for details.