# --- build ---
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/taas ./cmd/api

# --- runtime ---
FROM alpine:3.22
RUN adduser -D -u 10001 taas && mkdir /data && chown taas /data
USER taas
COPY --from=build /out/taas /usr/local/bin/taas
# Los datos (data.json) viven en un volumen para sobrevivir al contenedor.
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["taas", "-addr", ":8080", "-data", "/data/data.json"]
