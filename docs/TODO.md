# 待办

已定方向但暂不实施的工作。开始前再确认范围，完成后从这里删除。

## 接通软件内增量更新（F-15）

现状：签名清单、按文件增量下载、`pending.json` 事务、启动恢复与回滚、版本管理页都已实现，`scripts/e2e-update.sh` 端到端通过；发版流水线在有密钥时生成 `updates.tar.gz`。但发布版用不了软件内更新：

- 仓库没有 `WPAPP_UPDATE_KEY`/`WPAPP_UPDATE_PUB` secret，发布版不含公钥；
- `config.update.feedURL` 默认为空，也没有托管的更新源（元数据为 `production_update_feed=not-configured`）；
- 更新只替换裸可执行文件，不能更新 macOS `.app`，签名后还会破坏签名完整性。

最小方案，先只做 Windows：

1. **密钥**：负责人用 `wpupdate keygen` 生成密钥，存为上述两个 GitHub secret。私钥只由负责人保管。
2. **更新源**：用 GitHub Pages 托管。Release 工作流在发布成功后，把 `updates.tar.gz` 解开，发布成 `<源>/<平台>/manifest.json` 加文件的目录结构。
3. **默认地址**：构建时把 Pages 地址写入默认 `feedURL`，用户仍可在设置里改。
4. **验证**：先用两个预发布版本在 Windows 实机上走一遍升级、再检查已是最新、回滚；通过后再开自动检查。
5. **macOS**：继续手动下载安装，版本管理页说明原因。`.app` 整包更新和签名完整性另立项。

依赖：D-02 签名主体只影响安装包签名，不阻塞更新清单签名。
