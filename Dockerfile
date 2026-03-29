FROM scratch
WORKDIR /app
COPY server /app/server
COPY static /app/static
EXPOSE 8080
ENTRYPOINT ["/app/server"]
