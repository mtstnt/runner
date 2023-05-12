run: build
	./bin/runner
	
build:
	go build -o ./bin/ .