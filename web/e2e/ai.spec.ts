import { expect, test, type APIRequestContext } from '@playwright/test'
import { createServer } from 'node:http'

// Every AI test lives in this one file. The settings the gateway reads are
// global, and Playwright runs separate files in parallel workers, so splitting
// them would let one test switch AI off underneath another.
let aiGatewayPort=0
const requestedBudgets:number[]=[]
const aiGateway=createServer((request,response)=>{
  if(request.method==='GET'&&request.url==='/v1/models'){
    // A vLLM style server publishes the context length, which is what the
    // reply budget is derived from.
    response.writeHead(200,{'Content-Type':'application/json'});response.end(JSON.stringify({data:[{id:'e2e-offline-model',max_model_len:16384}]}));return
  }
  if(request.method==='POST'&&request.url==='/v1/chat/completions'){
    let body=''
    request.on('data',chunk=>{body+=String(chunk)})
    request.on('end',()=>{
      const completion=JSON.parse(body) as {messages:Array<{role:string;content:string}>;max_tokens:number}
      const context=JSON.parse(completion.messages.at(-1)?.content||'{}') as {mode?:string}
      requestedBudgets.push((completion as unknown as {max_tokens:number}).max_tokens)
      const plan=context.mode==='anomaly'
        ?{summary:'이상치 한 건',explanation:'A1은 표본이 적어 검토가 필요합니다.',findings:[{row:1,column:1,severity:'warning',title:'검토 값',description:'비교 표본이 적어 수동 검토가 필요합니다.'}],changes:[]}
        :context.mode==='clean'
          ?{summary:'숫자 형식 정제',explanation:'A1을 문자열 숫자로 표준화합니다.',findings:[],changes:[{row:1,column:1,value:'5'}]}
          :{summary:'A1을 두 배로 계산',explanation:'B1에 A1의 두 배 수식을 제안합니다.',findings:[],changes:[{row:1,column:2,formula:'=A1*2'}]}
      response.writeHead(200,{'Content-Type':'application/json'});response.end(JSON.stringify({choices:[{message:{content:JSON.stringify(plan)},finish_reason:'stop'}],usage:{prompt_tokens:640,completion_tokens:96}}))
    })
    return
  }
  response.writeHead(404);response.end()
})
test.beforeAll(async()=>{await new Promise<void>((resolve,reject)=>{aiGateway.once('error',reject);aiGateway.listen(0,'0.0.0.0',()=>{const address=aiGateway.address();if(typeof address==='object'&&address){aiGatewayPort=address.port;resolve()}else reject(new Error('AI test gateway did not bind'))})})})
test.afterAll(async()=>{await new Promise<void>(resolve=>aiGateway.close(()=>resolve()))})


// The AI settings are global, so every test here sets the values it asserts and
// puts the defaults back afterwards instead of trusting what it inherits.
const put=(request:APIRequestContext,key:string,value:unknown,value_type:'string'|'number'|'boolean')=>
  request.put(`/api/v1/admin/settings/${key}`,{data:{key,value,value_type}})

async function enableAI(request:APIRequestContext,maxInputCells:number){
  await put(request,'ai.gateway_url','http://127.0.0.1:9/v1','string')
  await put(request,'ai.model','corp-llm-8b','string')
  await put(request,'ai.max_input_cells',maxInputCells,'number')
  await put(request,'ai.enabled',true,'boolean')
}

test.afterEach(async ({ request }) => {
  await put(request,'ai.enabled',false,'boolean')
  await put(request,'ai.max_input_cells',200,'number')
})

test('the AI panel scrolls, explains itself and shows the exact prompt', async ({ page, request }) => {
  await enableAI(request,200)
  const workbook=await request.post('/api/v1/workbooks',{data:{template_id:'monthly-sales'}}).then(response=>response.json())
  await page.goto(`/workbooks/${workbook.id}`)
  await page.waitForSelector('.grid-canvas')
  await page.waitForTimeout(800)
  await page.locator('.name-box').fill('A3:F9')
  await page.keyboard.press('Enter')

  const panel=page.getByLabel('AI 도우미 패널')
  await expect(panel.getByText(/A3:F9 · 42셀만 모델에 전달/)).toBeVisible()

  // The panel content is taller than the panel, and the container scrolls.
  const scroller=panel.locator('.ai-scroll')
  await expect.poll(async()=>scroller.evaluate(element=>element.scrollHeight>element.clientHeight)).toBe(true)
  await scroller.evaluate(element=>element.scrollTo(0,element.scrollHeight))
  expect(await scroller.evaluate(element=>element.scrollTop)).toBeGreaterThan(0)

  // The guide says what happens to the data and how approval works.
  await panel.getByRole('button',{name:'사용 가이드 열기'}).click()
  const guide=page.getByLabel('AI 사용 가이드')
  await expect(guide).toContainText('승인해야 적용됩니다.')
  await expect(guide).toContainText('전송되지 않는 것')
  await panel.getByRole('button',{name:'사용 가이드 닫기'}).click()

  // The prompt disclosure shows the real system prompt and the real payload.
  await panel.getByRole('radio',{name:/데이터 정제/}).click()
  await panel.getByRole('button',{name:/모델에 보내는 내용 보기/}).click()
  const disclosure=panel.locator('.ai-disclosure-body')
  await expect(disclosure).toContainText('kanpic')
  await expect(disclosure).toContainText('"mode": "clean"')
  await expect(disclosure).toContainText('"selected_range": "A3:F9"')
  await expect(disclosure.locator('.ai-prompt-meta')).toContainText('셀 42개')
  await expect(disclosure.locator('.ai-prompt-meta')).toContainText('corp-llm-8b')
})

test('the AI panel refuses a selection that exceeds the cell budget', async ({ page, request }) => {
  await enableAI(request,10)
  // The settings are global, so the state this test depends on is asserted.
  expect(await request.get('/api/v1/ai/config').then(response=>response.json())).toMatchObject({enabled:true,max_input_cells:10})
  const workbook=await request.post('/api/v1/workbooks',{data:{title:`AI 상한 ${Date.now()}`}}).then(response=>response.json())
  await page.goto(`/workbooks/${workbook.id}`)
  await page.waitForSelector('.grid-canvas')
  await page.waitForTimeout(800)
  await page.locator('.name-box').fill('A1:Z100')
  await page.keyboard.press('Enter')

  const panel=page.getByLabel('AI 도우미 패널')
  await expect(panel.getByText(/한 번에 최대 10셀까지/)).toBeVisible()
  await panel.getByRole('textbox',{name:'AI 요청'}).fill('요약해줘')
  await expect(panel.getByRole('button',{name:/분석 및 계획 미리보기/})).toBeDisabled()
})

test('the prompt preview refuses a workbook the caller cannot read', async ({ request }) => {
  await enableAI(request,200)
  const workbook=await request.post('/api/v1/workbooks',{
    headers:{'X-Kanpic-Actor':'ai.owner@corp.example'},data:{title:`AI 권한 ${Date.now()}`},
  }).then(response=>response.json())
  const body={workbook_id:workbook.id,sheet_id:workbook.sheets[0].id,range:'A1:B2',mode:'summarize',request:'요약',base_version:workbook.version}

  const owner=await request.post('/api/v1/ai/prompt:preview',{headers:{'X-Kanpic-Actor':'ai.owner@corp.example'},data:body})
  expect(owner.status()).toBe(200)
  const stranger=await request.post('/api/v1/ai/prompt:preview',{headers:{'X-Kanpic-Actor':'ai.stranger@corp.example'},data:body})
  expect(stranger.status()).toBe(403)
  const plan=await request.post('/api/v1/ai/actions:plan',{
    headers:{'X-Kanpic-Actor':'ai.stranger@corp.example'},data:{...body,idempotency_key:`stranger-${Date.now()}`},
  })
  expect(plan.status()).toBe(403)
})

test('plans, analyzes, cleans, audits, and undoes offline AI actions', async ({ page }) => {
  const versions=await page.request.get('/api/v1/admin/settings/versions').then(response=>response.json())
  const restoreRevision=versions.items[0].revision as number
  const gatewayHost=process.env.KANPIC_E2E_GATEWAY_HOST||'127.0.0.1'
  const putSetting=(key:string,value:unknown,value_type:'string'|'number'|'boolean',secret=false)=>page.request.put(`/api/v1/admin/settings/${key}`,{data:{key,value,value_type,secret,description:`E2E ${key}`}})
  let workbookId=''
  try{
    expect((await putSetting('ai.gateway_url',`http://${gatewayHost}:${aiGatewayPort}/v1`,'string')).ok()).toBe(true)
    expect((await putSetting('ai.model','e2e-offline-model','string')).ok()).toBe(true)
    expect((await putSetting('ai.max_input_cells',20,'number')).ok()).toBe(true)
    expect((await putSetting('ai.enabled',true,'boolean')).ok()).toBe(true)
    const tested=await page.request.post('/api/v1/admin/settings:test',{data:{}}).then(response=>response.json())
    expect(tested.items.find((item:{name:string})=>item.name==='사내 LLM Gateway')).toMatchObject({success:true})

    const created=await page.request.post('/api/v1/workbooks',{data:{title:`AI 안전 실행 ${Date.now()}`,workspace_id:'default'}}).then(response=>response.json())
    workbookId=created.id as string
    const sheetId=created.sheets[0].id as string
    const seeded=await page.request.patch(`/api/v1/sheets/${sheetId}/cells:batch`,{data:{base_version:1,idempotency_key:`ai-e2e-seed-${workbookId}`,cells:[{row:1,column:1,value:5}]}}).then(response=>response.json())
    await page.goto(`/workbooks/${workbookId}`)
    await page.getByRole('combobox',{name:'이름 상자'}).fill('A1:B1')
    await page.getByRole('combobox',{name:'이름 상자'}).press('Enter')
    const panel=page.getByRole('complementary',{name:'AI 도우미 패널'})
    await expect(panel.getByText(/A1:B1 · 2셀만 모델에 전달/)).toBeVisible()
    await panel.getByRole('radio',{name:/수식 생성/}).click()
    await panel.getByRole('textbox',{name:'AI 요청'}).fill('B1에 A1의 두 배 수식을 넣어줘')
    await panel.getByRole('button',{name:'계획 미리보기'}).click()
    await expect(panel.getByText('A1을 두 배로 계산')).toBeVisible()
    await expect(panel.getByText('=A1*2')).toBeVisible()
    let before=await page.request.get(`/api/v1/sheets/${sheetId}/ranges/B1`).then(response=>response.json())
    expect(before.items).toHaveLength(0)
    await panel.getByRole('button',{name:/검토한 계획 승인/}).click()
    await expect(panel.getByText('승인한 변경이 적용되었습니다.')).toBeVisible()
    // The reply budget comes from the model's published context length instead
    // of a fixed guess, and the token cost is reported back.
    expect(requestedBudgets[0]).toBeGreaterThan(8_192)
    expect(requestedBudgets[0]).toBeLessThan(16_384)
    await expect(panel.getByText(/응답 96토큰/)).toBeVisible()
    await expect.poll(async()=>{const range=await page.request.get(`/api/v1/sheets/${sheetId}/ranges/B1`).then(response=>response.json());return range.items[0]?.formula}).toBe('=A1*2')
    const actions=await page.request.get(`/api/v1/workbooks/${workbookId}/ai/actions`).then(response=>response.json())
    expect(actions.items).toHaveLength(1)
    const audited=await page.request.get(`/api/v1/ai/actions/${actions.items[0].id}`).then(response=>response.json())
    expect(audited.events.map((event:{tool_name:string})=>event.tool_name)).toEqual(['range.read','formula.set'])
    await panel.getByRole('button',{name:'Undo'}).click()
    await expect(panel.getByText('AI 변경을 새 서버 버전으로 되돌렸습니다.')).toBeVisible()
    await expect.poll(async()=>{const range=await page.request.get(`/api/v1/sheets/${sheetId}/ranges/B1`).then(response=>response.json());return range.items.length}).toBe(0)

    await panel.getByRole('button',{name:'새 요청 작성'}).click()
    await panel.getByRole('radio',{name:/이상치 탐지/}).click()
    await panel.getByRole('textbox',{name:'AI 요청'}).fill('선택 범위의 이상치를 찾아줘')
    await panel.getByRole('button',{name:'분석 및 계획 미리보기'}).click()
    await expect(panel.getByText('탐지 완료',{exact:true})).toBeVisible()
    await expect(panel.getByText('검토 값')).toBeVisible()
    await expect(panel.getByText('현재: 5')).toBeVisible()
    await expect(panel.getByRole('button',{name:/검토한 계획 승인/})).toHaveCount(0)

    await panel.getByRole('button',{name:'새 요청 작성'}).click()
    await panel.getByRole('radio',{name:/데이터 정제/}).click()
    await panel.getByRole('textbox',{name:'AI 요청'}).fill('A1의 숫자 형식을 정제해줘')
    await panel.getByRole('button',{name:'분석 및 계획 미리보기'}).click()
    await expect(panel.getByText('숫자 형식 정제')).toBeVisible()
    expect((await page.request.get(`/api/v1/sheets/${sheetId}/ranges/A1`).then(response=>response.json())).items[0].value).toBe(5)
    await panel.getByRole('button',{name:/검토한 계획 승인/}).click()
    await expect(panel.getByText('승인한 변경이 적용되었습니다.')).toBeVisible()
    await expect.poll(async()=>{const range=await page.request.get(`/api/v1/sheets/${sheetId}/ranges/A1`).then(response=>response.json());return range.items[0]?.value}).toBe('5')
    const latestActions=await page.request.get(`/api/v1/workbooks/${workbookId}/ai/actions`).then(response=>response.json())
    const cleanAction=latestActions.items.find((item:{mode:string})=>item.mode==='clean')
    const cleanAudit=await page.request.get(`/api/v1/ai/actions/${cleanAction.id}`).then(response=>response.json())
    expect(cleanAudit.events.map((event:{tool_name:string})=>event.tool_name)).toEqual(['range.read','data.clean'])
    await panel.getByRole('button',{name:'Undo'}).click()
    await expect.poll(async()=>{const range=await page.request.get(`/api/v1/sheets/${sheetId}/ranges/A1`).then(response=>response.json());return range.items[0]?.value}).toBe(5)
    expect(seeded.server_version).toBe(2)
  }finally{
    if(workbookId)await page.request.delete(`/api/v1/workbooks/${workbookId}`)
    await page.request.post(`/api/v1/admin/settings/versions/${restoreRevision}:restore`,{data:{}})
  }
})
