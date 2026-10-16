# Development Rules

- Use English Conventional Commit messages.
- Commit each completed and independently verifiable work item before starting the next one.
- Keep every commit scoped to one repository and one concern.
- Run the relevant tests before committing.
- Write all code comments and documentation comments in English.
- Always request elevated permissions before running `gh`.
- Real modem calls and SMS tests may target only `13800000000` or `13500000000`.
- Validate every real modem test destination with `scripts/check-real-test-number.sh` before issuing `ATD` or `AT+CMGS`.
