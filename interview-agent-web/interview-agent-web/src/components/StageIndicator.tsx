/**
 * @author: 公众号：IT杨秀才
 * @doc:StudentCoach - Student Ability Growth Agent
 */

import { useChatStore } from '../store/chatStore'

const stages = [
  { key: 'ability_analysis', label: '解析标准' },
  { key: 'student_profile_analysis', label: '诊断起点' },
  { key: 'rag_retrieval', label: '检索题库' },
  { key: 'question_plan', label: '规划训练' },
  { key: 'training', label: '能力训练' },
  { key: 'evaluation', label: '形成性评价' },
  { key: 'growth_plan', label: '成长计划' },
]

export function StageIndicator() {
  const { currentStage, isTraining } = useChatStore()

  if (!isTraining && !currentStage) return null

  const currentIndex = stages.findIndex((s) => currentStage.startsWith(s.key))

  return (
    <div className="flex items-center gap-1 px-4 py-2 bg-white border-b overflow-x-auto">
      {stages.map((s, i) => {
        const done = i < currentIndex || currentStage === 'completed'
        const active = i === currentIndex
        return (
          <div key={s.key} className="flex items-center">
            <div
              className={`text-xs px-2 py-1 rounded-full whitespace-nowrap ${
                done
                  ? 'bg-green-100 text-green-700'
                  : active
                    ? 'bg-blue-100 text-blue-700 font-medium'
                    : 'bg-gray-100 text-gray-400'
              }`}
            >
              {s.label}
            </div>
            {i < stages.length - 1 && (
              <div className={`w-4 h-px mx-0.5 ${done ? 'bg-green-300' : 'bg-gray-200'}`} />
            )}
          </div>
        )
      })}
    </div>
  )
}
