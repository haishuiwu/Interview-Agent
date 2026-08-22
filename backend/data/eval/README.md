# RAG 离线评估数据集

本目录用于验证学生能力训练题库的检索质量，配合 `internal/rag/evaluation_metrics.go` 与 `cmd/eval.go` 使用。

## 数据文件

- `manifest.json`：五类学生能力训练案例的题目清单。
- `dataset_v1.sample.json`：每类能力一条标注示例。
- `dataset_v1.json`：覆盖全部十五个训练案例的正式数据集。
- `reports/`：评测报告输出目录。

## 样本格式

```json
{
  "id": "eval_001",
  "query": "调查相关性 因果推断 替代解释 结论边界",
  "relevant_doc_ids": ["eval_critical-thinking_003"],
  "topic": "批判性思维",
  "difficulty": "hard",
  "note": "评价因果结论边界"
}
```

`query` 应模拟训练规划阶段生成的检索词，包含能力、情境和关键方法，不写成长篇完整问题。`relevant_doc_ids` 必须来自 `manifest.json`。

## 当前覆盖

- 逻辑思维：条件推理、异常识别、因果论证。
- 沟通表达：对象化解释、协作回应、结构化汇报。
- 问题解决：问题拆解、方案设计、小规模验证。
- 批判性思维：来源判断、观点比较、结论边界。
- 反思迁移：错误归因、过程复盘、策略迁移。

## 运行方式

```bash
go run ./cmd/ eval --prepare
go run ./cmd/ eval --gen-dataset
go run ./cmd/ eval -dataset data/eval/dataset_v1.json -out data/eval/reports -note "student-ability-baseline"
```

`--prepare` 会解析 `data/questions/<skill>/<skill>.md`，写入 Milvus/BM25，并重新生成 `manifest.json`。正式评测前应确认新 manifest 的 ID 与数据集标注一致。
