# Contributing

Thanks for your interest in YardMaster.

## Issues and suggestions are welcome

- **Bugs:** open an issue with what you did, what you expected and what happened instead. Include
  the YardMaster and Switchyard versions (shown at the bottom of the sidebar) and the relevant
  lines from `docker logs`. Leave out API keys, tokens and passwords.
- **Ideas:** open an issue describing the problem you want solved, not only the solution. It helps
  to know how your team uses YardMaster.
- **Providers and models:** reports of what worked, or didn't, with a provider or model are
  especially useful. [docs/tested.md](docs/tested.md) lists what has been tried so far.
- **Security problems:** don't open an issue. Follow [SECURITY.md](SECURITY.md) instead.

## Pull requests aren't accepted yet

YardMaster is licensed under the AGPL-3.0, and its author keeps the copyright so that other
licensing terms stay possible later. Accepting code would need a contributor license agreement,
which isn't in place yet. Until it is, pull requests will be closed without being merged, however
good they are. An issue describing the change is the best way to get it in.

## Working on it locally

[docs/development.md](docs/development.md) explains how to build and run YardMaster, and
[docs/architecture.md](docs/architecture.md) how the code is organized. `make check` runs every
check the code must pass.
