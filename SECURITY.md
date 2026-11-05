# Security policy

laraport connects to your servers over SSH and runs commands in your containers, so a security bug here can matter a lot. Thank you for reporting one.

## Reporting a vulnerability

Please do not open a public issue. Use [Report a vulnerability](https://github.com/alexbabintsev/laraport/security/advisories/new) on GitHub, which reaches the maintainer privately. The same goes for the site, [laraport.app](https://laraport.app).

Say what the problem is, how to reproduce it and which version you ran (`laraport --version`). You will get an answer within a week.

## Supported versions

Fixes go into the latest release. Upgrade to it before reporting, if you can.

## What counts

- Commands or SQL that run without the confirmation laraport promises
- Anything that gets past a *Read-only* server
- Secrets that reach the command line, logs or files without mode `0600`
- Injection through container names, log lines or other data from a server

How laraport handles your servers and data: [laraport.app/docs/security](https://laraport.app/docs/security/).
