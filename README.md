# personal-aO3-mirror
Personal Local Mirror for AO3 - https://archiveofourown.org/

## AI Disclaimer

This project's code was developed with the help of AI.

No AI was used to scrape, read, or view any works from AO3. The scraper is ordinary Go code
with no AI or machine-learning components, and nothing it collects is used for AI training.

During development the AI assistant is mechanically blocked from fanwork content: it cannot
read the mirrored data, query the databases, or fetch AO3 pages. Parsers are built from AO3's
open-source code ([otwcode/otwarchive](https://github.com/otwcode/otwarchive)) and synthetic,
hand-written test fixtures. See [`CLAUDE.md`](CLAUDE.md) and
[`.claude/hooks/guard.py`](.claude/hooks/guard.py) for the rules and their enforcement.
