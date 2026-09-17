.PHONY: build test run app install icon clean

build:
	MACOSX_DEPLOYMENT_TARGET=14.0 go build -o dist/awsm-desktop .

test:
	go test ./...

# Serve the panel over HTTP so it can be opened in a browser with dev tools.
run:
	go run -tags devserver ./devserver

app:
	./build/package.sh

# Draw every status bar icon on a light and a dark menu bar, at its real size
# and magnified. The icon is not a template image any more, so its colours are
# this program's decision and worth looking at before shipping them.
icon:
	go run ./build/preview dist/icons.png
	@echo "wrote dist/icons.png"

# Replace the running copy in /Applications.
install: app
	rm -rf /Applications/awsm.app
	cp -R dist/awsm.app /Applications/
	@echo "installed; open it with: open /Applications/awsm.app"

clean:
	rm -rf dist
