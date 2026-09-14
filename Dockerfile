FROM golang:1.26-alpine AS builder
ARG ALPINE_MIRROR=mirrors.aliyun.com
ARG GOPROXY=https://goproxy.cn,direct
RUN sed -i "s/dl-cdn.alpinelinux.org/${ALPINE_MIRROR}/g" /etc/apk/repositories
RUN go env -w GOPROXY=${GOPROXY}

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app .

FROM alpine:3.22
ARG ALPINE_MIRROR=mirrors.aliyun.com
RUN sed -i "s/dl-cdn.alpinelinux.org/${ALPINE_MIRROR}/g" /etc/apk/repositories
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/app /app/app
# db/ holds the schema snapshot location; the Go DSL migrations are compiled
# into the binary itself.
COPY --from=builder /src/db /app/db

ENV AIRWAY_ENV=production
ENV PORT=1905
ENV TZ="Asia/Shanghai"

EXPOSE 1905

CMD ["/app/app"]
