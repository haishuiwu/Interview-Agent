# StudentCoach Evaluation Benchmark

该目录提供完全离线的自动评测，不访问真实模型 API、MCP、数据库或外部网络。

运行完整项目测试：

```bash
go test ./...
```

查看可读评测报告：

```bash
go test -v ./evaluation
```

评测指标：

| 测试 | 指标 | 判定方式 |
|---|---|---|
| `skill_selection_test.go` | Skill Accuracy | 现有 Skill Registry 的选择与标注 Skill 完全一致 |
| `tool_calling_test.go` | Tool Selection Accuracy | StudentCoach 实际 Tool 调用链与标注链完全一致 |
| `ability_diagnosis_test.go` | Diagnosis Accuracy | Go 聚合后的能力维度和分数均与标注一致 |
| `growth_loop_test.go` | Growth Loop Success Rate | 能力分更新、GrowthRecord 保存和第二轮 Skill 推荐全部成功 |

测试数据统一位于 `testdata/`。每项测试会输出 Markdown 表格形式的通过数、样例数和准确率。

## 当前离线基线

| 指标 | 通过/样例 | 结果 |
|---|---:|---:|
| Skill Accuracy | 15/15 | 100.00% |
| Tool Selection Accuracy | 7/7 | 100.00% |
| Diagnosis Accuracy | 6/6 | 100.00% |
| Growth Loop Success Rate | 1/1 | 100.00% |

合计 29 条标注样例。该基线使用确定性的 mock LLM，衡量的是 Agent 编排、选择、诊断聚合和成长闭环逻辑，不代表真实模型在开放输入上的泛化准确率。
