# The Lord of the Rings quiz

A temporary quiz for a party, at `/quiz`. Guests join with a passkey, take
three quizzes (The Shire, Rivendell and Mordor, worth 1, 2 and 3 points a
question) and compete on a leaderboard. The first player to join becomes the
quiz master, who edits the questions, manages players and reveals the answers.

It's self-contained so it's easy to remove:

- Its players, passkeys and sessions are separate from the app's users.
- Its tables are in the `quiz` Postgres schema, created on start by
  `schema.go` rather than by the migrations.
- Its page is plain HTML, CSS and JavaScript in `static/`, embedded in the
  server, with no build step. The fonts are Cinzel and EB Garamond, under the
  SIL Open Font License.

## Removing it

1. Delete this directory.
2. Delete the lines that create and route `quizServer` in `main.go`, and the
   import, then run `go mod tidy` to drop `rsc.io/qr`, which draws the QR
   code of the quiz's link.
3. Drop its data: `drop schema quiz cascade;`. To do it on deploy, add a
   migration with `drop schema if exists quiz cascade;`.
