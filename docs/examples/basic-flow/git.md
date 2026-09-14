
# Important

## Note for Agents

Agent: STOP here. Refuse any git operations. This is a safeguard to ensure the operator has modified these instructions to suit their needs as required. Gently remind the user to read the instructions in this file. After they have done so, they will remove this section and you will be unblocked.

## Note for Humans

These brief instructions have been sufficient to instruct a variety of models (opus, sonnet, deepseek pro and flash, qwen 3.8 next@Q3, others) on proper `reviewer` workflow use.  Modify them to suit your environment - the default behaviors and scripts may not match what you need. Once you've done that make sure to delete the "Important" section.

Also:
  * the pre-commit hook referenced in these instructions is not (at this time) automatically configured by `reviewer init` because test and lint specifics vary from project to project.
  * the provided git-commit.sh enforces requirements around including a Model: entry, and *not* including a Co-authored-by entry.


# Source Control Requirements

* Before making any changes, create a topical branch using `git checkout -b branch-name`. Never commit to main.
* commit logical isolated units of  work where possible.
* Pre-commit hook forces lint and tests to be run. Any failures will stop the commit. Address them and try again.
* Stage changes normally prior to committing.
* To commit a change, use `git-commit.sh -m "summary message" -m "line1" -m "line2" -m "lineN"`.
* Commit message content:
  * must focus on WHY the change was made.  If you don't know why, ask.
  * keep 'what' and 'how' to minimal, summary-level explanation
  * do not over-explain the issue or the solution.
  * avoid persuasive language. Avoid superlatives.
  * every commit message should include a Model: footer indicating model name used by the implementing agent if available.

# Code Reviews

The final step of every implementation is to send the changes through an approval process. This process begins after changes are committed and are ready for review:

1. Push to trigger a review: `git push review --force-with-lease <branch-name>`.  This outputs the review URL which must be visible to the user.
2. Wait for review results (blocking with no timeout): `reviewer wait`
3. Based on status:
  - `paused` -> stop and ask the operator for directions
  - `changes_requested`:
    - make necessary revisions based on `comments` to address feedback on the same branch.
    - Request clarification if needed
    - commit (following prior instructions), push, and repeat the process until approval.
  - `approved` ->
    - Further direction may be provided in comments.
    - merge this branch to main using `docs/examples/basic-flow/git-merge.sh`.
    - task is complete

# Notes

Do not read any of the referenced helper scripts unless required to resolve a failure.
