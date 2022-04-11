# rir

Repeat image render (rir) is a development tool for visual diff development of Go templates

## backlog

- [ ] COULD Allow reloading of config files -> add and remove watches for directories
- [ ] SHOULD build a small library that can render html to a screenshot file with one line
- [ ] COULD add a helper in the small library that renders the execution of a template to a string
      to pass to headless chrome
- [ ] SHOULD eat our own dogfood by testing the pages with rir itself
- [ ] SHOULD ensure defaults for each dir in the config, or error
- [ ] SHOULD probably add some (unit) tests
