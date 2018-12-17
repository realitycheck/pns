FROM golang as builder

ARG GO_PACKAGE
ARG GO_APP_FILE

WORKDIR /go/src/${GO_PACKAGE}
ADD . .
RUN CGO_ENABLED=0 GOOS=linux GO_APP_FILE=${GO_APP_FILE} GO_OUTPUT_FILE=/app make build


FROM scratch

COPY --from=builder /app /
ENV PATH /
STOPSIGNAL SIGTERM
ENTRYPOINT [ "app" ]