FROM alpine:3.3
RUN apk add --no-cache openssl ca-certificates
ADD koha-rfidhub2 /
CMD ["/koha-rfidhub2"]
