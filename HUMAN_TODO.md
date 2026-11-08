# HUMAN_TODO.md

These are decisions only a human can make. Agents left placeholders marked
`TODO(human)` and continued; none of these block code.

- [ ] **LICENSE**: confirm the OSS license for SyncFS (repo currently ships Apache-2.0 from initial commit). Confirm copyright holder.
- [ ] **Security contact**: add a real address for `SECURITY.md` (currently `TODO(human)` placeholder).
- [ ] **GitHub settings**: repo visibility, branch protection, required reviews, CODEOWNERS.
- [ ] **Release publishing**: GoReleaser release (`goreleaser release --clean`) and tag creation (`git tag v0.1.0 && git push origin v0.1.0`) — agents never push tags.
- [ ] **Docker Hub / registry** credentials if images are published.
- [ ] **M5 target app**: pick the first qualification workload (Jira/Confluence DC version or repository manager) for the integration suite.
- [ ] **Enterprise CA vs offline CA** for the first production deployment.
- [ ] **At-rest encryption**: LUKS acceptability for the first deployment.
- [ ] **Quota model**: volume-only vs per-directory/user quota policy.
