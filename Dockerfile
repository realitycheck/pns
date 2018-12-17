FROM golang as builder

ARG GO_PACKAGE

WORKDIR /go/src/${GO_PACKAGE}
ADD . .
RUN CGO_ENABLED=0 GOOS=linux GO_OUTPUT_FILE=/pns make


FROM scratch

COPY --from=builder /pns /
ENV PATH /
STOPSIGNAL SIGTERM
ENTRYPOINT [ "pns" ]