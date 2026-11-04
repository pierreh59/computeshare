# Image de production de l'orchestrateur ComputeShare.
# Build multi-etape : compilation Go dans une image de build, image
# finale minimale (alpine, utilisateur non-root, certificats CA pour
# les appels sortants).

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY services/orchestrator/go.mod services/orchestrator/go.sum ./
RUN go mod download
COPY services/orchestrator/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/orchestrator .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 1000 app
COPY --from=build /out/orchestrator /usr/local/bin/orchestrator
USER app
EXPOSE 8081
ENTRYPOINT ["orchestrator"]
