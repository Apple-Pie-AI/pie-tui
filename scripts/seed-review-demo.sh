#!/usr/bin/env bash
# Seed a fake reviewed PR so the review-comments screen can be driven offline.
#
# Nothing here touches GitHub: the session's `repo` column is left EMPTY on
# purpose, which is what makes `pollable()` false, so the hub never shells out to
# `gh` for a PR that does not exist. That is the whole trick that lets this run
# on a plane.
#
# Usage:  PIE_HOME=/tmp/pie-demo scripts/seed-review-demo.sh
set -euo pipefail

PIE_HOME="${PIE_HOME:-/tmp/pie-demo}"
DB="$PIE_HOME/state.db"
TICKET="${TICKET:-PIE-207}"
PR="https://github.com/acme/android/pull/482"
NOW="$(date +%s)"

mkdir -p "$PIE_HOME"

# The binary creates the schema (and runs migrations) on first open, so let it
# make the DB rather than hand-writing CREATE TABLE here and drifting from it.
if [ ! -f "$DB" ]; then
  echo "creating $DB via pie…"
  PIE_HOME="$PIE_HOME" "${PIE:-./pie}" status >/dev/null 2>&1 || true
fi
[ -f "$DB" ] || { echo "no $DB - run ./pie status with PIE_HOME set first" >&2; exit 1; }

sqlite3 "$DB" <<SQL
DELETE FROM pr_comments WHERE ticket = '$TICKET';
DELETE FROM sessions    WHERE ticket = '$TICKET';

-- repo = '' keeps the hub from polling GitHub for this fake PR.
INSERT INTO sessions (ticket, repo, state, branch, worktree, pr_url, summary, created_at, updated_at)
VALUES ('$TICKET', '', 'review', 'ai/pie-207-login-retry', '', '$PR',
        'Retry login on transient network failures', $NOW, $NOW);

-- 3 humans, 4 bots, 1 already-addressed. The mix is the point: it exercises
-- both filters and the counts on both buttons at once.
INSERT INTO pr_comments
 (id, ticket, pr_url, kind, author, is_bot, path, line, body, url, diff_hunk,
  outdated, resolved, addressed_at, addressed_sha, last_comment, first_seen_at, updated_at)
VALUES
 ('T1','$TICKET','$PR','thread','alice',0,'app/src/main/java/com/acme/LoginViewModel.kt',42,
  'Don''t swallow the exception here — this hides real auth failures from Crashlytics. Log it and rethrow.',
  '$PR#discussion_r1',
  '@@ -38,7 +38,11 @@ class LoginViewModel {
     fun login() {
         } catch (e: IOException) {
-            emit(Error)
+            emit(Error)
+            return',
  0,0,0,'','c1',$NOW,$NOW),

 ('T2','$TICKET','$PR','thread','bob',0,'app/src/main/java/com/acme/LoginViewModel.kt',88,
  'nit: rename \`loading\` to \`isLoading\` for consistency with the rest of the file.',
  '$PR#discussion_r2','@@ -86,3 +86,3 @@
-    var loading = false
+    var loading = false',0,0,0,'','c2',$NOW,$NOW),

 ('T3','$TICKET','$PR','review','carol',0,'',0,
  'Overall this is close, but the retry policy should live in the repository layer, not the view model.',
  '$PR#pullrequestreview-9','',0,0,0,'','c3',$NOW,$NOW),

 ('B1','$TICKET','$PR','thread','coderabbitai[bot]',1,'app/build.gradle.kts',12,
  'Consider extracting this dependency version into the version catalog.','$PR#discussion_r4','',0,0,0,'','c4',$NOW,$NOW),
 ('B2','$TICKET','$PR','thread','coderabbitai[bot]',1,'app/src/main/java/com/acme/AuthRepo.kt',7,
  'Nitpick: this import appears unused.','$PR#discussion_r5','',0,0,0,'','c5',$NOW,$NOW),
 ('B3','$TICKET','$PR','thread','github-copilot[bot]',1,'app/src/main/java/com/acme/AuthRepo.kt',31,
  'Potential null dereference on \`session\`.','$PR#discussion_r6','',0,0,0,'','c6',$NOW,$NOW),
 ('B4','$TICKET','$PR','thread','sonarcloud[bot]',1,'app/src/main/java/com/acme/LoginViewModel.kt',60,
  'Cognitive complexity of this method is 18 (limit 15).','$PR#discussion_r7','',0,0,0,'','c7',$NOW,$NOW),

 ('D1','$TICKET','$PR','thread','dave',0,'app/src/main/java/com/acme/Net.kt',5,
  'This timeout should be configurable.','$PR#discussion_r8','',0,0,$NOW,'a1b2c3d4','c8',$NOW,$NOW);
SQL

echo "seeded $TICKET into $DB"
sqlite3 "$DB" "SELECT '  humans: '||COUNT(*) FROM pr_comments WHERE ticket='$TICKET' AND is_bot=0 AND addressed_at=0;
               SELECT '  bots:   '||COUNT(*) FROM pr_comments WHERE ticket='$TICKET' AND is_bot=1;
               SELECT '  done:   '||COUNT(*) FROM pr_comments WHERE ticket='$TICKET' AND addressed_at>0;"
echo
echo "now run:  PIE_HOME=$PIE_HOME ./pie monitor"
