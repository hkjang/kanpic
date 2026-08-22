import { AlertTriangle, X } from 'lucide-react'
import { address } from '../lib/api'
import { explainFormulaError } from '../lib/formulaError'
import type { MutationResult } from '../types'

/**
 * 방금 한 편집이 다른 곳의 수식을 깨뜨렸다고 알린다. 행 하나를 지워
 * 열두 곳이 `#REF!` 가 되어도 화면은 아무 말이 없었다. 편집을 막지 않도록
 * 대화상자가 아니라 옆으로 비켜선 안내로 둔다.
 */
export function FormulaIssueNotice({issues,onOpen,onClose}:{
  issues:MutationResult['formula_errors']
  onOpen:(issue:MutationResult['formula_errors'][number])=>void
  onClose:()=>void
}){
  if(issues.length===0)return null
  const first=issues[0]
  const explanation=explainFormulaError(first.code)
  const where=address(first.row,first.column)
  return <div className="formula-issue" role="status">
    <AlertTriangle/>
    <div>
      <strong>{issues.length>1?`이 편집으로 수식 ${issues.length}곳이 오류가 되었습니다`:'이 편집으로 수식 한 곳이 오류가 되었습니다'}</strong>
      <small>{where} {first.code}{explanation?` · ${explanation.summary}`:''}</small>
    </div>
    <button className="link-button" onClick={()=>onOpen(first)}>보기</button>
    <button className="issue-close" aria-label="알림 닫기" onClick={onClose}><X/></button>
  </div>
}
