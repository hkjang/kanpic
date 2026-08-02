import { QueryClient,QueryClientProvider } from '@tanstack/react-query'
import { cleanup,fireEvent,render,screen,waitFor } from '@testing-library/react'
import { afterEach,describe,expect,it,vi } from 'vitest'
import type { Automation,AutomationExecutionResult,AutomationPreview,AutomationRun,MutationResult,Sheet } from '../types'
import { AutomationPanel } from './AutomationPanel'

const sheet:Sheet={id:'sheet-1',workbook_id:'book-1',name:'Data',position:0,hidden:false,layout:{revision:1,frozen_rows:0,frozen_columns:0},created_at:'2026-08-02T00:00:00Z'}
const automation:Automation={id:'automation-1',workbook_id:'book-1',name:'상태 완료',enabled:true,trigger:{type:'manual'},action:{type:'set_value',sheet_id:sheet.id,range:'B1',value:'완료'},revision:1,created_by:'alice',updated_by:'alice',created_at:'2026-08-02T00:00:00Z',updated_at:'2026-08-02T00:00:00Z'}
const preview:AutomationPreview={automation_id:automation.id,workbook_id:automation.workbook_id,base_version:2,changes:[{row:1,column:2,address:'B1',before:{},after:{value:'완료'}}]}
const operation:MutationResult={operation_id:'operation-1',workbook_id:'book-1',sheet_id:sheet.id,base_version:2,server_version:3,applied_cells:1,recalculated_cells:[],formula_errors:[],validation_warnings:[],duplicate:false,conflicts:[]}
const run:AutomationRun={id:'run-1',automation_id:automation.id,workbook_id:'book-1',actor_id:'alice',trigger_type:'manual',status:'succeeded',base_version:2,action:automation.action,operation_id:operation.operation_id,operation,started_at:'2026-08-02T00:01:00Z',completed_at:'2026-08-02T00:01:01Z',updated_at:'2026-08-02T00:01:01Z'}
const executed:AutomationExecutionResult={run,operation,changes:[{row:1,column:2,value:'완료'}]}

afterEach(()=>{cleanup();vi.restoreAllMocks();vi.unstubAllGlobals()})

function renderPanel(onExecuted=vi.fn()){
  const client=new QueryClient({defaultOptions:{queries:{retry:false,gcTime:0}}})
  render(<QueryClientProvider client={client}><AutomationPanel workbookId="book-1" sheets={[sheet]} activeSheetId={sheet.id} selectionRange="B1" onClose={vi.fn()} onExecuted={onExecuted}/></QueryClientProvider>)
  return onExecuted
}

describe('AutomationPanel',()=>{
  it('creates an automation, previews exact changes, then explicitly runs it',async()=>{
    let items:Automation[]=[]
    let runs:AutomationRun[]=[]
    const fetchMock=vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{
      const path=String(input),method=init?.method??'GET'
      if(path==='/api/v1/workbooks/book-1/automations'&&method==='GET')return json({items})
      if(path==='/api/v1/workbooks/book-1/automations'&&method==='POST'){
        const body=JSON.parse(String(init?.body)) as Record<string,unknown>
        expect(body).toMatchObject({name:'상태 완료',enabled:true,trigger:{type:'manual'},action:{type:'set_value',sheet_id:sheet.id,range:'B1',value:'완료'}})
        expect(body.idempotency_key).toBeTruthy()
        items=[automation]
        return json(automation,201)
      }
      if(path==='/api/v1/automations/automation-1:test')return json(preview)
      if(path==='/api/v1/automations/automation-1/runs?limit=12')return json({items:runs})
      if(path==='/api/v1/automations/automation-1:run'){
        const body=JSON.parse(String(init?.body)) as Record<string,unknown>
        expect(body).toMatchObject({expected_revision:1,client_id:expect.any(String)})
        expect(body.idempotency_key).toBeTruthy()
        runs=[run]
        return json(executed)
      }
      return json({},404)
    })
    vi.stubGlobal('fetch',fetchMock)
    const onExecuted=renderPanel()
    expect(await screen.findByText('등록된 자동화가 없습니다')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button',{name:/새 자동화/}))
    fireEvent.change(screen.getByLabelText('자동화 이름'),{target:{value:'상태 완료'}})
    fireEvent.click(screen.getByRole('button',{name:/저장 후 검증/}))
    expect(await screen.findByText('실행 미리보기')).toBeInTheDocument()
    expect(screen.getByText('(빈 셀)')).toBeInTheDocument()
    expect(screen.getAllByText('완료').length).toBeGreaterThan(0)
    expect(onExecuted).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button',{name:/검토한 자동화 실행/}))
    await waitFor(()=>expect(onExecuted).toHaveBeenCalledWith(executed))
  })

  it('shows run history and sends Undo through a new idempotent operation',async()=>{
    let currentRun=run
    const undoOperation:MutationResult={...operation,operation_id:'operation-undo',base_version:3,server_version:4}
    const undone:AutomationExecutionResult={run:{...run,status:'undone',undo_operation_id:undoOperation.operation_id,undo_operation:undoOperation},operation:undoOperation}
    const fetchMock=vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{
      const path=String(input)
      if(path==='/api/v1/workbooks/book-1/automations')return json({items:[automation]})
      if(path==='/api/v1/automations/automation-1:test')return json(preview)
      if(path==='/api/v1/automations/automation-1/runs?limit=12')return json({items:[currentRun]})
      if(path==='/api/v1/automation-runs/run-1:undo'){
        const body=JSON.parse(String(init?.body)) as Record<string,unknown>
        expect(body.idempotency_key).toBeTruthy()
        expect(body.client_id).toBeTruthy()
        currentRun=undone.run
        return json(undone)
      }
      return json({},404)
    })
    vi.stubGlobal('fetch',fetchMock)
    const onExecuted=renderPanel()
    const validate=await screen.findByRole('button',{name:/검증/})
    fireEvent.click(validate)
    expect(await screen.findByText('실행 이력')).toBeInTheDocument()
    const undoButton=await screen.findByRole('button',{name:/Undo/})
    fireEvent.click(undoButton)
    await waitFor(()=>expect(onExecuted).toHaveBeenCalledWith(undone))
  })

  it('creates a timezone-aware Cron schedule and shows its next run',async()=>{
    const scheduled:Automation={...automation,id:'schedule-1',name:'매시간 갱신',trigger:{type:'schedule',cron:'0 * * * *',timezone:'Asia/Seoul'},next_run_at:'2026-08-02T01:00:00Z'}
    let items:Automation[]=[]
    const fetchMock=vi.fn(async(input:RequestInfo|URL,init?:RequestInit)=>{
      const path=String(input),method=init?.method??'GET'
      if(path==='/api/v1/workbooks/book-1/automations'&&method==='GET')return json({items})
      if(path==='/api/v1/workbooks/book-1/automations'&&method==='POST'){
        const body=JSON.parse(String(init?.body)) as Record<string,unknown>
        expect(body).toMatchObject({name:'매시간 갱신',trigger:{type:'schedule',cron:'0 * * * *',timezone:'Asia/Seoul'}})
        items=[scheduled]
        return json(scheduled,201)
      }
      if(path==='/api/v1/automations/schedule-1:test')return json({...preview,automation_id:scheduled.id})
      if(path==='/api/v1/automations/schedule-1/runs?limit=12')return json({items:[]})
      return json({},404)
    })
    vi.stubGlobal('fetch',fetchMock)
    renderPanel()
    await screen.findByText('등록된 자동화가 없습니다')
    fireEvent.click(screen.getByRole('button',{name:/새 자동화/}))
    fireEvent.change(screen.getByLabelText('자동화 이름'),{target:{value:'매시간 갱신'}})
    fireEvent.change(screen.getByLabelText('자동화 트리거'),{target:{value:'schedule'}})
    fireEvent.change(screen.getByLabelText('스케줄 프리셋'),{target:{value:'0 * * * *'}})
    fireEvent.click(screen.getByRole('button',{name:/저장 후 검증/}))
    expect(await screen.findByText(/다음 실행/)).toBeInTheDocument()
    expect(screen.getByText(/0 \* \* \* \*/)).toBeInTheDocument()
  })
})

function json(value:unknown,status=200){return new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})}
