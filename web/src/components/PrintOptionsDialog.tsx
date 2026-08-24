import { Printer } from 'lucide-react'
import { useState } from 'react'
import type { PrintFit,PrintMargin,PrintOrientation } from '../lib/printSheet'
import './PrintOptionsDialog.css'
import { useDialog } from '../lib/useDialog'

export type PrintChoice={orientation:PrintOrientation;margin:PrintMargin;fit:PrintFit}

const STORE_KEY='kanpic.print.options'

// 지난번에 고른 것을 기억한다. 같은 표를 여러 번 찍는 일이 흔하고, 매번
// 같은 것을 다시 고르게 하는 것은 일을 늘리는 것이다.
export function loadPrintChoice():PrintChoice{
  const fallback:PrintChoice={orientation:'portrait',margin:'normal',fit:'none'}
  try{
    const stored=window.localStorage.getItem(STORE_KEY)
    if(!stored)return fallback
    const parsed=JSON.parse(stored) as Partial<PrintChoice>
    return {
      orientation:parsed.orientation==='landscape'?'landscape':'portrait',
      margin:parsed.margin==='narrow'||parsed.margin==='wide'?parsed.margin:'normal',
      fit:parsed.fit==='width'?'width':'none',
    }
  }catch{return fallback}
}

export function PrintOptionsDialog({onClose,onPrint}:{onClose:()=>void;onPrint:(choice:PrintChoice)=>void}){
  const [choice,setChoice]=useState<PrintChoice>(loadPrintChoice)
  const dialog=useDialog<HTMLElement>(onClose)
  const print=()=>{
    try{window.localStorage.setItem(STORE_KEY,JSON.stringify(choice))}catch{/* 저장하지 못해도 인쇄는 한다 */}
    onPrint(choice)
  }
  return <div className="modal-backdrop"><div className="modal print-options-modal" role="dialog" ref={dialog as React.RefObject<any>} aria-modal="true" aria-label="인쇄 설정">
    <header><div><Printer/><div><h2>인쇄 설정</h2><p>종이 방향과 여백을 정하고 넓은 표를 한 장 너비에 맞춥니다.</p></div></div><button aria-label="인쇄 설정 닫기" onClick={onClose}>×</button></header>
    <div className="print-options-body">
      <fieldset><legend>방향</legend>
        <label><input type="radio" name="orientation" checked={choice.orientation==='portrait'} onChange={()=>setChoice(current=>({...current,orientation:'portrait'}))}/> 세로</label>
        <label><input type="radio" name="orientation" checked={choice.orientation==='landscape'} onChange={()=>setChoice(current=>({...current,orientation:'landscape'}))}/> 가로</label>
      </fieldset>
      <fieldset><legend>여백</legend>
        <label><input type="radio" name="margin" checked={choice.margin==='narrow'} onChange={()=>setChoice(current=>({...current,margin:'narrow'}))}/> 좁게</label>
        <label><input type="radio" name="margin" checked={choice.margin==='normal'} onChange={()=>setChoice(current=>({...current,margin:'normal'}))}/> 보통</label>
        <label><input type="radio" name="margin" checked={choice.margin==='wide'} onChange={()=>setChoice(current=>({...current,margin:'wide'}))}/> 넓게</label>
      </fieldset>
      <fieldset><legend>넓은 표</legend>
        <label><input type="radio" name="fit" checked={choice.fit==='none'} onChange={()=>setChoice(current=>({...current,fit:'none'}))}/> 열을 다음 장으로 넘기기</label>
        <label><input type="radio" name="fit" checked={choice.fit==='width'} onChange={()=>setChoice(current=>({...current,fit:'width'}))}/> 한 장 너비에 맞추기</label>
      </fieldset>
      <p className="print-options-note">한 장 너비에 맞추면 글자가 작아집니다. 열이 아주 많으면 가로 방향과 함께 쓰세요.</p>
    </div>
    <div className="modal-actions"><span/><button className="secondary" onClick={onClose}>취소</button><button className="primary" onClick={print}>인쇄</button></div>
  </div></div>
}
