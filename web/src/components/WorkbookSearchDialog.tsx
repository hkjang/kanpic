import { FileSpreadsheet, Search, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/api'
import type { WorkbookSearchMatch, WorkbookSearchResult } from '../types'
import './WorkbookSearchDialog.css'

function valueLabel(value:unknown){
  if(value==null)return '빈 값'
  if(typeof value==='string')return value
  try{return JSON.stringify(value)}catch{return String(value)}
}

function matchLabel(item:WorkbookSearchMatch){
  if(item.matched_fields.includes('formula')&&item.formula)return item.formula
  return valueLabel(item.value)
}

export function WorkbookSearchDialog({open,workbookId,version,onClose,onNavigate}:{open:boolean;workbookId:string;version:number;onClose:()=>void;onNavigate:(item:WorkbookSearchMatch)=>void}){
  const [query,setQuery]=useState(''),[result,setResult]=useState<WorkbookSearchResult>(),[loading,setLoading]=useState(false),[loadingMore,setLoadingMore]=useState(false),[error,setError]=useState(''),[activeIndex,setActiveIndex]=useState(0)
  const inputRef=useRef<HTMLInputElement>(null)
  useEffect(()=>{if(open){const timer=window.setTimeout(()=>inputRef.current?.focus(),0);return()=>window.clearTimeout(timer)}setActiveIndex(0)},[open])
  useEffect(()=>{
    if(!open)return
    const normalized=query.trim()
    if(!normalized){setResult(undefined);setError('');setLoading(false);return}
    const controller=new AbortController(),timer=window.setTimeout(()=>{
      setLoading(true);setError('')
      api<WorkbookSearchResult>(`/api/v1/workbooks/${workbookId}/search?q=${encodeURIComponent(normalized)}&limit=50`,{signal:controller.signal})
        .then(value=>{setResult(value);setActiveIndex(0)})
        .catch(reason=>{if(!controller.signal.aborted)setError(reason instanceof Error?reason.message:'검색하지 못했습니다.')})
        .finally(()=>{if(!controller.signal.aborted)setLoading(false)})
    },250)
    return()=>{window.clearTimeout(timer);controller.abort()}
  },[open,query,workbookId,version])
  useEffect(()=>{if(!open)return;const close=(event:KeyboardEvent)=>{if(event.key==='Escape')onClose()};window.addEventListener('keydown',close);return()=>window.removeEventListener('keydown',close)},[open,onClose])
  if(!open)return null
  const choose=(item:WorkbookSearchMatch)=>{onNavigate(item);onClose()}
  const loadMore=async()=>{if(result?.next_offset==null||loadingMore)return;setLoadingMore(true);setError('');try{const page=await api<WorkbookSearchResult>(`/api/v1/workbooks/${workbookId}/search?q=${encodeURIComponent(result.query)}&limit=50&offset=${result.next_offset}`);setResult(current=>current?.query===page.query?{...page,items:[...current.items,...page.items]}:current)}catch(reason){setError(reason instanceof Error?reason.message:'다음 결과를 불러오지 못했습니다.')}finally{setLoadingMore(false)}}
  const items=result?.items??[]
  return <div className="search-backdrop" role="presentation" onMouseDown={event=>{if(event.target===event.currentTarget)onClose()}}>
    <section className="workbook-search" role="dialog" aria-modal="true" aria-label="워크북 통합 검색">
      <div className="workbook-search-input"><Search/><input ref={inputRef} value={query} maxLength={256} placeholder="값 또는 수식 검색" aria-label="검색어" onChange={event=>setQuery(event.target.value)} onKeyDown={event=>{if(event.key==='ArrowDown'){setActiveIndex(index=>items.length?Math.min(items.length-1,index+1):0);event.preventDefault()}else if(event.key==='ArrowUp'){setActiveIndex(index=>Math.max(0,index-1));event.preventDefault()}else if(event.key==='Enter'&&items[activeIndex]){choose(items[activeIndex]);event.preventDefault()}}}/><kbd>ESC</kbd><button aria-label="검색 닫기" onClick={onClose}><X/></button></div>
      <div className="workbook-search-meta"><span>{loading?'서버 저장 상태를 검색하는 중…':result?`${result.items.length.toLocaleString()}개 결과${result.next_offset!=null?' 이상':''}`:'현재 워크북의 값과 수식을 검색합니다.'}</span>{result&&<small>워크북 v{result.workbook_version}</small>}</div>
      <div className="workbook-search-results" role="listbox" aria-label="검색 결과">
        {!loading&&query.trim()&&items.length===0&&!error&&<div className="workbook-search-empty"><Search/><strong>일치하는 셀이 없습니다.</strong><span>다른 검색어를 입력해 보세요.</span></div>}
        {!query.trim()&&<div className="workbook-search-empty"><FileSpreadsheet/><strong>워크북 전체 검색</strong><span>대소문자 구분 없이 셀 값과 저장된 수식을 찾습니다.</span></div>}
        {items.map((item,index)=><button key={`${item.sheet_id}:${item.address}`} role="option" aria-selected={index===activeIndex} className={index===activeIndex?'active':''} onMouseEnter={()=>setActiveIndex(index)} onClick={()=>choose(item)}><span className="search-location"><FileSpreadsheet/><strong>{item.sheet_name}</strong><code>{item.address}</code></span><span className="search-preview">{matchLabel(item)}</span><span className="search-fields">{item.matched_fields.map(field=><em key={field}>{field==='formula'?'수식':'값'}</em>)}</span></button>)}
      </div>
      {error&&<div className="workbook-search-error" role="alert">{error}</div>}
      {result?.next_offset!=null&&<button className="workbook-search-more" disabled={loadingMore} onClick={()=>void loadMore()}>{loadingMore?'불러오는 중…':'결과 더 보기'}</button>}
      <footer><span><kbd>↑</kbd><kbd>↓</kbd> 이동</span><span><kbd>Enter</kbd> 셀 열기</span><span>서버 권위 검색</span></footer>
    </section>
  </div>
}
