PYTHON ?= python3
VERSION ?= 0.0.0-dev
.PHONY: check test e2e build package manifests
check:
	$(PYTHON) scripts/check.py

test:
	$(PYTHON) scripts/check.py --go-only

e2e:
	$(PYTHON) scripts/check.py

build:
	$(PYTHON) scripts/check.py --build-only

package:
	$(PYTHON) scripts/package.py --version $(VERSION) --full

manifests:
	$(PYTHON) scripts/manifests.py --version $(VERSION)
