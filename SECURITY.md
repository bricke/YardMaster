# Security

YardMaster holds provider API keys and every user's gateway tokens, and it sits between your
team's tools and the model providers. Security reports are welcome and taken seriously.

## Reporting a vulnerability

Please report vulnerabilities privately, not in a public issue. On this repository's GitHub
page, open the **Security** tab and choose **Report a vulnerability**. Only the maintainer can
see the report.

Include what you can of:

- what an attacker can do, and what they need first (network access, a user account, an admin
  account);
- the steps to reproduce it, and the YardMaster and Switchyard versions (shown at the bottom of
  the sidebar);
- how YardMaster was set up: built-in sign-in or behind another proxy, and the HTTPS mode.

You'll get an answer within a week. Once a fix is released, the report can be made public, with
credit to you if you want it.

## Supported versions

YardMaster is young: fixes go into the latest release only.

## Scope

In scope is everything in this repository and the Docker image it builds, for example:

- sign-in, sessions, gateway tokens, the trusted-proxy mode and the roles they grant;
- anything that returns, logs or stores a secret in clear: provider keys, tokens, passwords;
- the gateway forwarding YardMaster's own credentials, or letting a caller claim to be someone
  else;
- the built-in certificate authority and the HTTPS setup;
- one user seeing another user's usage.

Out of scope:

- vulnerabilities in Switchyard itself: report those to
  [Switchyard](https://github.com/NVIDIA-NeMo/Switchyard);
- a model provider's behaviour, or what a model answers;
- attacks that need the admin account or write access to `/data`, which already control
  everything YardMaster does;
- running with HTTPS off on an untrusted network, which the UI warns about.

## Running YardMaster safely

[install.md](docs/install.md) covers the setup. In short: turn on HTTPS, set
`YARDMASTER_SECRET_KEY` so provider keys are encrypted at rest, don't publish the ports beyond
the network that needs them, and back up `/data` and the secret key separately.
