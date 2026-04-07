# Bead Map — Command Center Gaps

## Hierarchy

- `wg-08bec92b7db3` - EPIC: Command Center - Close the Gaps
  - **Phase 1: Auto-Dispatch Engine**
    - `wg-165543456e94` - tryAutoDispatch() method [HIGH]
    - `wg-fbc250c4b9cf` - Hook into agent status transitions [HIGH] (depends: wg-165543456e94)
    - `wg-361bc6a09e80` - Project setting toggle [HIGH]
    - `wg-8f3362e2c24c` - Dashboard toggle + indicator [HIGH] (depends: wg-361bc6a09e80)
  - **Phase 2: Alert Respawn + Bulk Assign**
    - `wg-b534e62fcd6a` - Respawn endpoint [HIGH]
    - `wg-8f03743eedcb` - Respawn button on alerts [HIGH] (depends: wg-b534e62fcd6a)
    - `wg-595ad663189e` - Bulk assign action [MEDIUM]
  - **Phase 3: Dependency Editing + Send to Agent**
    - `wg-ddc85709bcd1` - Dependency editor [MEDIUM]
    - `wg-dc5720f47dfb` - Send to Agent button [MEDIUM]
  - **Phase 4: Project View + Slash Commands**
    - `wg-8195bed504ab` - Per-project dashboard view [MEDIUM]
    - `wg-19e50fe4cce3` - /assign and /unassign commands [MEDIUM]

## GitHub Issues Auto-Created

| Task | Issue |
|------|-------|
| tryAutoDispatch() | [#14](https://github.com/maniginam/waggle/issues/14) |
| Project setting toggle | [#12](https://github.com/maniginam/waggle/issues/12) |
| Respawn endpoint | [#15](https://github.com/maniginam/waggle/issues/15) |
| Bulk assign | [#11](https://github.com/maniginam/waggle/issues/11) |
| Dependency editor | [#7](https://github.com/maniginam/waggle/issues/7) |
| Send to Agent | [#8](https://github.com/maniginam/waggle/issues/8) |
| /assign commands | [#9](https://github.com/maniginam/waggle/issues/9) |
