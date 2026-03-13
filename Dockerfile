## Build
FROM golang:1.25-bookworm AS build

WORKDIR /app

COPY . ./

RUN go mod download
RUN make build
RUN mkdir -p /app/runtime/data /app/runtime/log

## Deploy
FROM gcr.io/distroless/base-debian12:nonroot

WORKDIR /app

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build --chown=nonroot:nonroot /app/configs /app/configs
COPY --from=build --chown=nonroot:nonroot /app/output/server /app/server
COPY --from=build --chown=nonroot:nonroot /app/runtime/data /app/data
COPY --from=build --chown=nonroot:nonroot /app/runtime/log /app/log

ENV APP_MODE=prod
ENV APP_STORE_PATH=/app/data/crux.db
ENV DB_DSN=/app/data/crux.db?_fk=1
ENV HTTP_LOG_TO_STDOUT=true

VOLUME ["/app/data", "/app/log"]

EXPOSE 8082

ENTRYPOINT ["/app/server"]
