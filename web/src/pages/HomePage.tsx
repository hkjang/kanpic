import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Clock3, FilePlus2, Grid2X2, MoreHorizontal, Plus, Star } from 'lucide-react'
import { api } from '../lib/api'
import type { BuildInfo, Session, Workbook } from '../types'
import { AppHeader } from '../components/AppHeader'

export function HomePage({build,session}:{build?:BuildInfo;session?:Session}) {
  const client=useQueryClient();const [title,setTitle]=useState('')
  const workbooks=useQuery({queryKey:['workbooks'],queryFn:()=>api<{items:Workbook[]}>('/api/v1/workbooks')})
  const create=useMutation({mutationFn:()=>api<Workbook>('/api/v1/workbooks',{method:'POST',body:JSON.stringify({title:title.trim()||'제목 없는 워크북',workspace_id:'default'})}),onSuccess:(wb)=>{client.invalidateQueries({queryKey:['workbooks']});window.location.href=`/workbooks/${wb.id}`}})
  return <div className="page-shell"><AppHeader build={build} session={session}/><main className="home-content">
    <section className="home-title"><div><span className="eyebrow">WORKSPACE</span><h1>좋은 아침이에요.</h1><p>오늘도 데이터에서 더 나은 답을 만들어 보세요.</p></div><button className="primary" onClick={()=>create.mutate()}><Plus size={18}/> 새 워크북</button></section>
    <section className="quick-start"><div className="section-heading"><h2>빠른 시작</h2></div><div className="template-row">
      <button className="template blank" onClick={()=>create.mutate()}><span><FilePlus2/></span><strong>빈 워크북</strong><small>새로운 데이터 작업 시작</small></button>
      {['프로젝트 현황','월간 매출 분석','업무 요청 관리'].map((name,index)=><button className={`template template-${index}`} key={name} onClick={()=>{setTitle(name);setTimeout(()=>create.mutate(),0)}}><span><Grid2X2/></span><strong>{name}</strong><small>kanpic 기본 템플릿</small></button>)}
    </div></section>
    <section className="recent-section"><div className="section-heading"><h2>최근 워크북</h2><div className="segmented"><button className="active"><Clock3/> 최근</button><button><Star/> 즐겨찾기</button></div></div>
      {workbooks.isLoading?<div className="loading-card">워크북을 불러오는 중…</div>:workbooks.data?.items.length===0?<div className="empty-state"><FilePlus2/><h3>첫 워크북을 만들어 보세요</h3><p>셀 편집 내용은 자동으로 안전하게 저장됩니다.</p></div>:<div className="workbook-grid">{workbooks.data?.items.map(wb=><a href={`/workbooks/${wb.id}`} className="workbook-card" key={wb.id}><div className="workbook-preview"><Grid2X2/><span>{wb.sheets.length} sheets</span></div><div className="workbook-meta"><div><strong>{wb.title}</strong><small>{new Date(wb.updated_at).toLocaleString('ko-KR')} 수정</small></div><button aria-label="더보기"><MoreHorizontal/></button></div></a>)}</div>}
    </section>
  </main></div>
}
