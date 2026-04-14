.PHONY: docs-install docs-serve docs-build docs-clean

docs-install:
	pip install -r docs/requirements.txt

docs-serve:
	mkdocs serve

docs-build:
	mkdocs build --strict

docs-clean:
	rm -rf site/
