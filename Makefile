VENV := .venv

.PHONY: docs-install docs-serve docs-build docs-clean

docs-install:
	python3 -m venv $(VENV)
	$(VENV)/bin/pip install --quiet -r docs/requirements.txt

docs-serve:
	$(VENV)/bin/mkdocs serve

docs-build:
	$(VENV)/bin/mkdocs build --strict

docs-clean:
	rm -rf site/ $(VENV)
