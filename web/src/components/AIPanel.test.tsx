import { QueryClient,QueryClientProvider } from '@tanstack/react-query'
import { cleanup,fireEvent,render,screen,waitFor } from '@testing-library/react'
import { afterEach,describe,expect,it,vi } from 'vitest'
import type { AIAction,AIExecutionResult,MutationResult } from '../types'
import { AIPanel } from './AIPanel'

const planned:AIAction={id:'ai-1',workbook_id:'book-1',sheet_id:'sheet-1',actor_id:'alice',mode:'formula',range:'A1:B1',request:'B1에 두 배 수식',status:'planned',base_version:2,model:'offline-model',summary:'A1을 두 배로 계산',explanation:'B1에 A1의 두 배 수식을 제안합니다.',changes:[{row:1,column:2,address:'B1',before:{},after:{formula:'=A1*2'}}],findings:[],input_cell_count:2,revision:1,created_at:'2026-08-02T00:00:00Z',updated_at:'2026-08-02T00:00:00Z'}
const operation:MutationResult={operation_id:'op-ai',workbook_id:'book-1',sheet_id:'sheet-1',base_version:2,server_version:3,applied_cells:1,recalculated_cells:[],formula_errors:[],validation_warnings:[],duplicate:false,conflicts:[]}
const applied:AIExecutionResult={action:{...planned,status:'applied',revision:2,operation_id:'op-ai',operation},operation,changes:[{row:1,column:2,formula:'=A1*2'}]}

afterEach(()=>{cleanup();vi.restoreAllMocks();vi.unstubAllGlobals()})

function renderPanel(onExecuted=vi.fn()){
  const client=new QueryClient({defaultOptions:{queries:{retry:false,gcTime:0}}})
  render(<QueryClientProvider client={client}><AIPanel workbookId="book-1" sheetId="sheet-1" selectionRange="A1:B1" baseVersion={2} onClose={vi.fn()} onExecuted={onExecuted}/></QueryClientProvider>)
  return onExecuted
}

describe('AIPanel',()=>{
  it('keeps AI disabled until an administrator configures the internal gateway',async()=>{
    vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>new Response(JSON.stringify(String(input).includes('/ai/config')?{enabled:false,model:'offline',max_input_cells:200,max_changes:100}:{items:[]}),{status:200,headers:{'Content-Type':'application/json'}})))
    renderPanel()
    expect(await screen.findByText('AI가 아직 비활성화되어 있습니다.')).toBeInTheDocument()
    expect(screen.getByRole('link',{name:'관리자 AI 설정 열기'})).toHaveAttribute('href','/admin?tab=settings')
  })

  it('shows a non-destructive formula preview and applies only after approval',async()=>{
    let action=planned
    const fetchMock=vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{
      const path=String(input)
      if(path==='/api/v1/ai/config')return new Response(JSON.stringify({enabled:true,model:'offline-model',max_input_cells:200,max_changes:100}),{status:200,headers:{'Content-Type':'application/json'}})
      if(path==='/api/v1/workbooks/book-1/ai/actions?limit=8')return new Response(JSON.stringify({items:action?[action]:[]}),{status:200,headers:{'Content-Type':'application/json'}})
      if(path==='/api/v1/ai/actions:plan'){
        const body=JSON.parse(String(init?.body)) as Record<string,unknown>
        expect(body).toMatchObject({workbook_id:'book-1',sheet_id:'sheet-1',range:'A1:B1',mode:'formula',base_version:2})
        expect(body.idempotency_key).toBeTruthy()
        return new Response(JSON.stringify(planned),{status:201,headers:{'Content-Type':'application/json'}})
      }
      if(path==='/api/v1/ai/actions/ai-1:approve'){
        const body=JSON.parse(String(init?.body)) as Record<string,unknown>
        expect(body).toMatchObject({expected_revision:1,client_id:expect.any(String)})
        action=applied.action
        return new Response(JSON.stringify(applied),{status:200,headers:{'Content-Type':'application/json'}})
      }
      return new Response('{}',{status:404,headers:{'Content-Type':'application/json'}})
    })
    vi.stubGlobal('fetch',fetchMock)
    const onExecuted=renderPanel()
    expect(await screen.findByText('A1:B1만 모델에 전달')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('AI 요청'),{target:{value:'B1에 두 배 수식'}})
    fireEvent.click(screen.getByRole('button',{name:'분석 및 계획 미리보기'}))
    expect(await screen.findByText('A1을 두 배로 계산')).toBeInTheDocument()
    expect(screen.getByText('(빈 셀)')).toBeInTheDocument()
    expect(screen.getByText('=A1*2')).toBeInTheDocument()
    expect(onExecuted).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button',{name:/검토한 계획 승인/}))
    await waitFor(()=>expect(onExecuted).toHaveBeenCalledWith(applied))
    expect(await screen.findByText('승인한 변경이 적용되었습니다.')).toBeInTheDocument()
  })

  it('marks an explanation complete without exposing an approval action',async()=>{
    const explanation:AIAction={...planned,id:'ai-explain',mode:'explain',request:'설명해줘',status:'completed',summary:'선택 수식 설명',explanation:'A1의 값을 두 배로 계산합니다.',changes:[]}
    vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>{
      const path=String(input)
      if(path==='/api/v1/ai/config')return new Response(JSON.stringify({enabled:true,model:'offline-model',max_input_cells:200,max_changes:100}),{status:200,headers:{'Content-Type':'application/json'}})
      if(path==='/api/v1/workbooks/book-1/ai/actions?limit=8')return new Response(JSON.stringify({items:[]}),{status:200,headers:{'Content-Type':'application/json'}})
      if(path==='/api/v1/ai/actions:plan')return new Response(JSON.stringify(explanation),{status:201,headers:{'Content-Type':'application/json'}})
      return new Response('{}',{status:404,headers:{'Content-Type':'application/json'}})
    }))
    renderPanel()
    await screen.findByText('A1:B1만 모델에 전달')
    fireEvent.change(screen.getByLabelText('AI 작업 유형'),{target:{value:'explain'}})
    fireEvent.change(screen.getByLabelText('AI 요청'),{target:{value:'설명해줘'}})
    fireEvent.click(screen.getByRole('button',{name:'분석 및 계획 미리보기'}))
    expect(await screen.findByText('설명 완료')).toBeInTheDocument()
    expect(screen.getByText('읽기 전용 분석이 완료됐으며 워크북 변경은 없습니다.')).toBeInTheDocument()
    expect(screen.queryByRole('button',{name:/검토한 계획 승인/})).not.toBeInTheDocument()
  })

  it('renders cell-anchored anomaly findings without offering a write approval',async()=>{
    const anomaly:AIAction={...planned,id:'ai-anomaly',mode:'anomaly',request:'이상치 탐지',status:'completed',summary:'이상치 한 건',explanation:'B1이 다른 값보다 큽니다.',changes:[],findings:[{row:1,column:2,address:'B1',severity:'warning',title:'높은 값',description:'평균에서 크게 벗어났습니다.',cell:{value:999}}]}
    vi.stubGlobal('fetch',vi.fn(async(input:RequestInfo|URL)=>{
      const path=String(input)
      if(path==='/api/v1/ai/config')return new Response(JSON.stringify({enabled:true,model:'offline-model',max_input_cells:200,max_changes:100}),{status:200,headers:{'Content-Type':'application/json'}})
      if(path==='/api/v1/workbooks/book-1/ai/actions?limit=8')return new Response(JSON.stringify({items:[]}),{status:200,headers:{'Content-Type':'application/json'}})
      if(path==='/api/v1/ai/actions:plan')return new Response(JSON.stringify(anomaly),{status:201,headers:{'Content-Type':'application/json'}})
      return new Response('{}',{status:404,headers:{'Content-Type':'application/json'}})
    }))
    renderPanel()
    await screen.findByText('A1:B1만 모델에 전달')
    fireEvent.change(screen.getByLabelText('AI 작업 유형'),{target:{value:'anomaly'}})
    fireEvent.change(screen.getByLabelText('AI 요청'),{target:{value:'이상치 탐지'}})
    fireEvent.click(screen.getByRole('button',{name:'분석 및 계획 미리보기'}))
    expect(await screen.findByText('탐지 완료')).toBeInTheDocument()
    expect(screen.getByText('높은 값')).toBeInTheDocument()
    expect(screen.getByText('현재: 999')).toBeInTheDocument()
    expect(screen.queryByRole('button',{name:/검토한 계획 승인/})).not.toBeInTheDocument()
  })

  it('previews literal cleaning changes and writes only after approval',async()=>{
    const cleanPlan:AIAction={...planned,id:'ai-clean',mode:'clean',request:'공백 정제',summary:'문자열 공백 정제',changes:[{row:1,column:1,address:'A1',before:{value:'  Seoul  '},after:{value:'Seoul'}}]}
    const cleanOperation:MutationResult={...operation,operation_id:'op-clean'}
    const cleanApplied:AIExecutionResult={action:{...cleanPlan,status:'applied',revision:2,operation_id:'op-clean',operation:cleanOperation},operation:cleanOperation,changes:[{row:1,column:1,value:'Seoul'}]}
    const fetchMock=vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{
      const path=String(input)
      if(path==='/api/v1/ai/config')return new Response(JSON.stringify({enabled:true,model:'offline-model',max_input_cells:200,max_changes:100}),{status:200,headers:{'Content-Type':'application/json'}})
      if(path==='/api/v1/workbooks/book-1/ai/actions?limit=8')return new Response(JSON.stringify({items:[]}),{status:200,headers:{'Content-Type':'application/json'}})
      if(path==='/api/v1/ai/actions:plan'){
        expect(JSON.parse(String(init?.body))).toMatchObject({mode:'clean',range:'A1:B1'})
        return new Response(JSON.stringify(cleanPlan),{status:201,headers:{'Content-Type':'application/json'}})
      }
      if(path==='/api/v1/ai/actions/ai-clean:approve')return new Response(JSON.stringify(cleanApplied),{status:200,headers:{'Content-Type':'application/json'}})
      return new Response('{}',{status:404,headers:{'Content-Type':'application/json'}})
    })
    vi.stubGlobal('fetch',fetchMock)
    const onExecuted=renderPanel()
    await screen.findByText('A1:B1만 모델에 전달')
    fireEvent.change(screen.getByLabelText('AI 작업 유형'),{target:{value:'clean'}})
    fireEvent.change(screen.getByLabelText('AI 요청'),{target:{value:'공백 정제'}})
    fireEvent.click(screen.getByRole('button',{name:'분석 및 계획 미리보기'}))
    expect(await screen.findByText('문자열 공백 정제')).toBeInTheDocument()
    expect(screen.getByText('1셀 변경')).toBeInTheDocument()
    expect(onExecuted).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button',{name:/검토한 계획 승인/}))
    await waitFor(()=>expect(onExecuted).toHaveBeenCalledWith(cleanApplied))
  })
})
