import { api } from './api'

// 서비스 간 문서 넘기기(HANDOFF-STANDARD)의 보내는 쪽. 사람이 파일을
// 내려받지 않는다 — 표(claim)를 발급받아 받는 쪽의 /handoff 를 새 창에서
// 열면 그쪽이 여기서 직접 받아 간다.

export type HandoffFormat = 'csv' | 'xlsx'
export type HandoffTarget = { name: string; origin: string; formats: HandoffFormat[] }
export type HandoffClaim = { claim: string; source: string; filename: string; content_type: string; bytes: number; expires_at: string }

export const handoffFormatLabel: Record<HandoffFormat, string> = { csv: '현재 시트 CSV', xlsx: 'XLSX' }

/** 받는 쪽이 여는 주소. source 와 claim 은 질의에 실리므로 그대로 넣지 않는다. */
export function handoffAddress(target: Pick<HandoffTarget, 'origin'>, claim: Pick<HandoffClaim, 'source' | 'claim'>) {
  const address = new URL('/handoff', target.origin)
  address.searchParams.set('source', claim.source)
  address.searchParams.set('claim', claim.claim)
  return address.toString()
}

/** 표에 적는 문서 id. 시트 하나(CSV)는 워크북 뒤에 시트를 붙인다. */
export function handoffResource(workbookId: string, format: HandoffFormat, sheetId?: string) {
  return format === 'csv' && sheetId ? `${workbookId}/sheets/${sheetId}` : workbookId
}

/**
 * 표를 발급받아 새 창을 받는 쪽으로 보낸다. 창은 누르는 순간에 미리 열어
 * 둔다 — 표를 기다린 뒤에 열면 팝업 차단에 걸린다. 표를 받지 못하면 그
 * 창을 닫고 까닭을 던진다.
 */
export async function sendHandoff(target: HandoffTarget, resource: string, format: HandoffFormat, open: (address?: string) => { location: { href: string }; close: () => void } | null = address => window.open(address, '_blank')) {
  const popup = open()
  try {
    const issued = await api<HandoffClaim>('/api/v1/handoff/claims', { method: 'POST', body: JSON.stringify({ resource, format }) })
    const address = handoffAddress(target, issued)
    if (popup) popup.location.href = address
    else open(address)
    return issued
  } catch (error) {
    popup?.close()
    throw error
  }
}
