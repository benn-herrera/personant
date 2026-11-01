### Plan dispatchability

A work package is dispatchable only when it:

- names its scope in files, packages or symbols — never a span of design sections;
- carries the command and its answer, or the quoted line, for each fact it acts on;
- if it changes a type, a signature or a test double, names in its scope every file that references
  it — `git grep -l '<Name>' -- '*.go'`, test files included — and carries that command and its
  answer;
- states its done-line in runner targets;
- has received a usability review by the prompt engineer (its definition contains the criteria);
- has been revised by the architect on the prompt engineer's findings.

A plan whose packages lack these is not dispatchable. It is not offered for approval as a dispatch
plan, and none of its packages is dispatched.

