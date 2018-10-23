FROM golang:1.10 as builder

WORKDIR /go/src/app
COPY . .

RUN go get -d -v ./...
RUN go install -v ./...
RUN CGO_ENABLED=0 GOOS=linux go build

FROM alpine:3.7

RUN apk add --update --no-cache ca-certificates
COPY --from=builder /go/src/app/app /koha-rfidhub2
CMD ["/koha-rfidhub2"]
