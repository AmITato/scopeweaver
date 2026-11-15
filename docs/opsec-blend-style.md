# House-style fixture corpus

English (Korean, Simplified Chinese, Traditional Chinese, Japanese and Spanish editions follow in a
separate commit; this page is evidence for [opsec-blend.md](opsec-blend.md) §5, not a user guide).

This page records what the target actually does, next to what our agents did on the same engagement. It
is the fixture the parser and generator are tested against (`blend/blend_test.go:32-197`) and the
reference an operator reads before accepting a generated name. Each section gives their grammar with its
source, ours with a count and a sample, and the failure mode that follows from the gap.

Their side is quoted from the cloned repositories under `data/gh-clone/`, the intent mirrors under
`data/tasks/9/i4158/mirrors/`, and the runbook under `data/tasks/9/b300-runbook/`. Our side is quoted
from the `activity` table of the engagement database; the query shape is:

```sql
select detail from activity
where exploration_id in (4,5,9) and kind = 'tool_use' and detail ~ '<pattern>';
```

Counts marked §1 are the engagement measurement on [opsec-blend.md](opsec-blend.md) §1. Counts marked
SQL are reproducible with the shape above and the stated pattern, de-duplicated.

## branch

**Theirs.** A ticket key from a fixed set, a hyphen, a number, then a Title Case slug
(`data/gh-clone/devsecops-scripts/.github/workflows/jira-check.yml:40`):

```
if [[ ! "$BRANCH_NAME" =~ ^(DEV|TS|MKM|DS|DB|MES|APP)-[0-9]+.*$ ]]; then
```

98 of the 115 refs in `data/tasks/9/i4158/mirrors/devsecops-scripts.git` obey it, e.g.
`DEV-33620-Dev-Scripts`, `DEV-40628-Mirabel-Support-Automation-Scripts`. The default branch is
`development` in 19 of the 36 clones and `main` in 11 (`git -C data/gh-clone/<repo> log --all
--pretty=%D`); neither is ticket-prefixed, and both are exempt by name
(`jira-check.yml:30`). Six repositories use a non-standard default instead (`rebuild`,
`new-setup-branch`, `initial`, `dev`, `master`, `chore/cursor-pr-security-scan-mpnj6mxo`).

**Ours.** 63 branch names, 57 of them `i<NNNN>`-prefixed (SQL, `refs/heads/`, filtered to
`i[0-9]{4}|hotfix/|feature/`). Sample `i6343-cbs-20261113t1`, `hotfix/i6404-durable-verify`,
`feature/dev-6343-probe`. 1 of 47 matched the regex (§1). The intent prefix is the dominant shape, and
even our ticket-bearing names fail, because the ticket sits after a prefix and not at the start.

**Failure mode.** The `Validate branch name` step prints
`❌ Branch name must start with a valid JIRA ID (e.g., DEV-123, TS-456, APP-789)` and exits 1
(`jira-check.yml:41-42`). Every push of ours opened a red run attributed to whoever pushed, and the
branch sits apart from theirs in the branch picker.

## workflow

**Theirs.** Lowercase kebab, no prefix, named for the check it runs: `ci.yml`, `deploy.yml`,
`promotion-gate.yml`, `jira-check.yml`, `gitleaks.yml`, `issue-queue.yml`, `qa-retest.yml`
(`ls data/gh-clone/*/.github/workflows`). The display `name:` is either the same token (`name: ci`,
`name: promotion-gate`) or a Title Case sentence (`name: PR Security Analysis`, `name: JIRA ID Check`).

**Ours.** 76 distinct workflow filenames beginning `i<NNNN>` (SQL, `\.github/workflows/(i[0-9]{4}…)`).
Sample `i6343-probe-cbstaging-20261113t1.yml`, `i5970-op-sweep.yml`, `i6004-harvest.yml`; the `name:`
is the intent id (`name: i5970-op-sweep`).

**Failure mode.** In the Actions list our runs sort together under `i` and read as a separate family.
`i6343-probe-cbstaging-20261113t1.yml` carries two identifiers the target never uses (intent and
timestamp) where `promotion-gate.yml` carries a purpose.

## commit_subject

**Theirs.** `type(#issue): imperative clause`, with the PR number appended; `Merge …` is skipped by
their check. Of 38,232 subjects in `data/tasks/9/i4158/mirrors/events.git`, 9,884 are `fix(#…)`, 12,179
are merge commits, and only 83 contain a ticket id anywhere. Sample
`fix(#17690): remove the Help icon from the published event website (#17691)`.

Their validator accepts a ticket id anywhere in the subject, not only at the start
(`jira-check.yml:75`), and rejects the rest with
`❌ Commit message must contain a JIRA ID (e.g., DEV-123 Fix, DEV-123-Fix, DEV-123-QA-Smoke-Test)`
(`:80`).

**Ours.** 41 commit messages, 33 beginning `i<NNNN>` and 1 beginning with a ticket (SQL, `''message''`);
sample `i5883 phase3 billing`, `ci: temporary oidc probe (self-reverting)`, and the single conforming
`DEV-6343 add read-only OIDC environment probe workflow`.

**Failure mode.** In the JIRA-checked repository the push fails the commit step outright, and elsewhere
the history reads as bolt-on work: our subjects name an intent (`i6005 pass2`) where theirs name an
outcome.

## iam_user

**Theirs.** Three shapes, mostly lowercase: `<product>-<purpose>-<object>`
(`mkm-proxy-ecr-ecs-deployment`, `pmta-log-uploader`, `ai-editor-assets-uploader`), `svc-<purpose>`
(`svc-mkm-backup`), and person logins (`rvasu`, `bala`) or mail-shaped logins
(`gkdasika@mirabeltechnologies.com`). Legacy names keep an uppercase or CamelCase shape
(`MKM-LP-PROD-ROUTE53-USER`, `EmailVerificationOndemand`). The defender's own responder user is
`mirabel-security-audit-user` (`data/tasks/9/b300-runbook/IR_PRESENCE.md:16`); the census holds 27
users, 2 of them ours (`data/tasks/9/i5617/10-iam-census.json`).

**Ours.** 33 users created, 4 deleted (§1). Sample `i5574-operator-20261113T144338Z`,
`i5601-support-canary`, `i6395-scp-detail-probe`, `ci-deploy-automation`, `model-selfreplica`.

**Failure mode.** An `i<NNNN>` user is self-identifying and short-lived by reputation: the defender ran
`ListAccessKeys i5574-operator-20261113T144338Z` 5.5 minutes after creation and deleted the user about
20 minutes after it (`IR_PRESENCE.md:97-99`, `:112-113`, `:138-141`). None of our names contains a
product the account already knows.

## iam_role

**Theirs.** `mkm-<purpose>-role` (`mkm-chalice-role`, `mkm-ecs-task-execution-role`), legacy uppercase
(`AWS-DPA-METERING-ROLE`), or CDK-generated `<Product>-role-<suffix>`
(`BulkSpfDkimChecker-role-ux1pi7lk`); all in `data/tasks/9/i5617/10-iam-census.json`.

**Ours.** `i5655-sminvoke-gateway-role`, `i6376-durable-oidc-operator`, `i4533-sm-probe`
(SQL, `create-role --role-name`). The role we borrow most often, `EventsDeployScoped`, is the target's
own (`data/tasks/9/i4268/repo/.claude/plans/ev-14-scope-oidc-deploy-role.md:9`), not a name we produced.

**Failure mode.** Role creation is the loudest act in this account — the defender had already recorded
our `i5655-sminvoke-gateway-role` at 15:04Z and flagged it as exposed (`IR_PRESENCE.md:36`, `:141`). A
matching name would at least survive the first sort.

## tag_value

**Theirs.** Key `Name`, and for infrastructure also lowercase `product` and `env`; values are Title Case
or lowercase, never an intent id:
`{'Key': 'Name', 'Value': 'SolarWinds DPA'}` (`data/tasks/9/i5617/30-inst-i090.json`);
`{'Key': 'Name', 'Value': 'events-vpc'}, {'Key': 'product', 'Value': 'events'}, {'Key': 'env', 'Value':
'dev'}` (`data/tasks/9/i6297/vpcs.json`). The only tag key on that stopped instance is `Name`.

**Ours.** `Key=Name,Value=i6589-b200-e1d`, `Key=Project,Value=b300-8x`. The parent measured `Project=`
once against `Name=` 636 times (§1); the single prose use of `Project=` is
`data/tasks/9/b300-runbook/CANONICAL_8XB300_LAUNCH_RUNBOOK.md:82`. Both of our values start with an
intent id.

**Failure mode.** The defender's reconstruction query is literally `tag:Name=i4*`
(`IR_PRESENCE.md:66`, repeated at `:152`). A resource is therefore found by our prefix, not by the
product it belongs to, and `Project` — the one key that would group the kit — is nearly absent.

## sg_name

**Theirs.** `<product>-<purpose>` or `<product>-<component>-<hash>`: `events-db-sg`,
`events-migrate-runner-secrets-sg`, `events-compute-AlbSecurityGroup86A59E99-saOBt2t5AHrw`, plus
`default` (`data/tasks/9/i6297/sgs.json`).

**Ours.** 33 groups created, 2 deleted (§1). Sample `i6299-b300-ssh-187-14-51-76`, `i3920-ssh`,
`i4424-b300` (SQL, `create-security-group --group-name`).

**Failure mode.** Ours names the intent and sometimes the operator IP; theirs names the workload. A
reader cannot tell from `i3920-ssh` that it belongs to the same estate as `events-db-sg`.

## sg_description

**Theirs.** A lowercase sentence describing the rule and its intent, often with a ` - ` clause:
`Postgres 5432 - ingress added per-consumer, never open`, `events export worker tasks - no ingress`,
`migrate-runner - HTTPS out to the Secrets Manager endpoint only` (`data/tasks/9/i6297/sgs.json`).

**Ours.** `SSH only from operator egress 187.14.51.76/32 - i6483 b3001`, `i3920 ssh proxy`
(SQL, `--description`). The sentence shape is right; the tail is an intent id.

**Failure mode.** Their descriptions describe the estate; ours date the object to one intent. When the
intent is forgotten the description stops meaning anything.

## marker

**Theirs.** Their own tooling signs with stock agents — `Boto3/1.44.2` for the scripted sweep and
`aws-cli/1.36.26` for manual triage (`IR_PRESENCE.md:44`). Their workflows are named for their function,
and their commit identity is a person (`Mark-Mirabel <mark@mirabeltechnologies.com>`).

**Ours.** `scopeweaver-blend/1` as the harvest user agent (`blend/harvest_github.go:28`), git identity
`i5587-canary <i5587@example.invalid>`, and host markers of the form `i5587 host=<hostname>
run=<run_id>` (SQL, `MARKER ===`).

**Failure mode.** Every marker is a fresh token the target has never seen. `i5587@example.invalid` is
unmistakably not a corporate address, and `scopeweaver-blend/1` announces the tool by name in every
harvest request.

## s3_key

**Theirs.** `<purpose>/<date>/<server>/<object>.<ext>` with the date as `YYYY_MM_DD` or `YYYY-MM-DD`. The
backup layout is uniform:
`prod-backups/2026_10_04/AWSSQL1New/saveon.bak`
(`data/tasks/9/b300-runbook/SQLDB_BACKUP_S3_EXPLOIT.md:57-61`). Keys read on the same buckets include
`Full-Backups/code/2026-11-11/mmprod.tar.gz` and
`Client1027/EmergencyBackup/Production_1027_20240913091850.csv` (SQL, `s3://`).

**Ours.** `i2913/_probe.txt`, `i2271_payload.tar.gz`, `i4165-put.txt`, `i2268/out_full.tar`
(SQL, `s3://<bucket>/`).

**Failure mode.** A leading `_` or the word `payload` puts our objects outside every dated prefix the
lifecycle rules and the restore tooling understand.

## keypair (adjacent)

**Theirs.** `US-EAST-EMAILVAL` — uppercase, region and function, no date
(`data/tasks/9/i5617/30-inst-i090.json`, cited at `IR_PRESENCE.md:103`).

**Ours.** 31 keypairs created, 2 deleted (§1). Sample `i3920-w2-key`, `i4424-b300`, `i1987-tmp` (SQL,
`create-key-pair --key-name`).

**Failure mode.** No `blend` class covers keypairs yet, so the name is generated by hand and falls back
to the intent prefix — the same failure as `branch`, on the object with the longest lifetime.

## What the corpus asks of the generator

- Lead with the fixed token, then prose: `{TICKET}-{Slug}`, not `i<NNNN>-{Slug}`.
- Keep the extension and the trailing sentence punctuation; they carry meaning
  (`blend/parse.go:226-244`).
- Prefer the target's own product token over an intent id wherever a value is free text.
- Never let a marker be the only distinguishing part of a name.
