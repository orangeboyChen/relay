# Repository History Migration

This monorepo preserves the commits previously stored in four repositories:

| Component | Previous repository | Previous `main` tip | Imported commits |
| --- | --- | --- | ---: |
| Server | `orangeboyChen/relay-control` | `b72ef56598a40478f26b7fa4fd7e1702c79f7c49` | 96 |
| Device | `orangeboyChen/relay-node` | `6c606db6cf3eeff2ec8275123f44893fe1a5fd70` | 138 |
| iOS | `orangeboyChen/relay-ios` | `22c42416d0766bac950b9eb6e73e2aa583434497` | 327 across all retained branches |
| Protocol | `orangeboyChen/relay-protocol` | `7cdce0a5cc719d7005091373b0fb904f68be6f11` | 2 |

The 563 imported commits were rewritten into one linear history because adding component directory prefixes and a single parent chain necessarily changes commit hashes. Author, committer, timestamps, and commit messages were preserved. The `refs/notes/import` Git notes namespace records the source component and original commit hash for every imported commit.

Legacy release tags use component-qualified names such as `server/v0.1.7`, `device/v0.1.14`, and `protocol/v0.1.0` to avoid collisions.

