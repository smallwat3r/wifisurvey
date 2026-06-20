BIN := wifisurvey

# Pass flags through, e.g. make survey ARGS="--ssid BT-RZA3ZX"
ARGS ?=

.PHONY: build survey analyse plot sample test fmt vet check clean

build: $(BIN)

$(BIN): $(wildcard *.go)
	go build -o $(BIN) .

survey: $(BIN)
	./$(BIN) survey $(ARGS)

analyse: $(BIN)
	./$(BIN) analyse $(ARGS)

# analyse then render the figure to PDF (needs gnuplot). e.g.
# make plot CSV=survey.csv GRAPH=img/survey.gp
CSV ?= survey.csv
GRAPH ?= survey.gp
plot: $(BIN)
	./$(BIN) analyse --csv $(CSV) --graph $(GRAPH)
	cd $(dir $(GRAPH)) && gnuplot $(notdir $(GRAPH))

# regenerate the sample figure PDF from the local sample (needs gnuplot)
sample: $(BIN)
	./$(BIN) analyse --csv sample-survey.csv --graph img/sample.gp
	cd img && gnuplot sample.gp

test:
	go test ./...

fmt:
	gofmt -w *.go

vet:
	go vet ./...

check: fmt vet test

clean:
	rm -f $(BIN)
