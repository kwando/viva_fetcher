build:
    go build -o viva_fetcher main.go
    GOOS=linux GOARCH=arm64 go build -o viva_fetcher_arm64 main.go
