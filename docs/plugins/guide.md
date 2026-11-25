# Writing a hamstack plugin (draft)

You need: a program that serves a web page on a port, a GitHub repository that publishes releases, and this repository's
validator. Worked example throughout: **rigctlWeb**, a Go server with an embedded single-page app that talks to rigctld.

## 1. Shape the program

1. **Listen where told.** Take an address flag or `PORT` variable; default to `127.0.0.1`. Never bind `0.0.0.0` by default.
2. **Accept a base path.** hamstack serves you at `/plugin/<id>/`. Add a flag (e.g. `-base-path`) or variable, normalise
   it (leading slash, no trailing slash), and serve everything under it: pages, `/api`, WebSocket, static files.
3. **No login of your own.** hamstack's login protects the page. If your program has a token or password mode, make it
   optional and off in plugin mode.
4. **Talk to the radio through rigctld.** Connect as a client to hamstack's rigctld (`needs.rig: "rigctld"`; the address
   is provided in the environment as `HAMSTACK_RIGCTLD`, `host:port`). Do not open the serial port.
5. **Health endpoint.** `GET /health` (or any path) answers 200 when you can serve.
6. **Stop cleanly.** Handle SIGTERM: stop accepting, release resources, and if you can transmit, key off first.
7. **Keep data in `{data_dir}` and settings in `{config_dir}`.** Nothing under the home folder outside them.

## 2. Publish releases

Tag `vX.Y.Z`; attach, per platform, a binary named by one template (rigctlWeb: `rigctlweb-<tag>-<os>-<arch>`), and a
`sha256sums.txt` listing them in `sha256sum` format. A program that prints its version (`-version`) lets hamstack tell
installed from current.

## 3. Write the manifest

Start from [examples/rigctlweb.json](examples/rigctlweb.json), change the ids, repository and flags to yours, then:

    hamstack plugin validate hamstack-plugin.json

Each problem names the field and what to change. Fix until it says `valid`.

## 4. Test it yourself

Before hamstack's runtime exists, run your program the way hamstack will:

    mkdir -p /tmp/p/data /tmp/p/config
    ./myplugin -listen 127.0.0.1:18080 -base-path /plugin/myplugin &
    curl -fsS http://127.0.0.1:18080/plugin/myplugin/api/health
    curl -fsS http://127.0.0.1:18080/plugin/myplugin/ | head        # links must start with /plugin/myplugin/
    kill -TERM %1; wait                                              # exits within 5 s, radio unkeyed

Open it through a reverse proxy that keeps the prefix (for example Caddy `handle /plugin/myplugin/* { reverse_proxy
127.0.0.1:18080 }`) and click through every page with the browser console open: a 404 on `/api/...` or a missing
asset means something is absolute.

## 5. Single-page apps

Build with a base path that is not fixed at build time, or build with relative asset URLs. SvelteKit: `paths.relative:
true` plus fetches from `./api`. Vite: `base: './'`. If a build-time base is unavoidable, serve `index.html` with the
prefix injected at start-up.

## 6. Submit

Open an issue on hamstack with the repository link and the validator output. Until the runtime exists, a plugin is
listed under "known plugins" in the docs and installed by hand.
