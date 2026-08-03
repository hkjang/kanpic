import { expect, test, type APIRequestContext, type Browser, type Page } from '@playwright/test'

// The API resolves the actor from X-Kanpic-Actor when no identity provider is
// configured, so a browser context with that header behaves as another user.
async function pageAs(browser:Browser,actor:string){
  const context=await browser.newContext({extraHTTPHeaders:{'X-Kanpic-Actor':actor},viewport:{width:1400,height:900}})
  return{context,page:await context.newPage()}
}

async function createWorkbook(request:APIRequestContext,title:string){
  const workbook=await request.post('/api/v1/workbooks',{data:{title}}).then(response=>response.json())
  await request.patch(`/api/v1/sheets/${workbook.sheets[0].id}/cells:batch`,{data:{
    base_version:workbook.version,idempotency_key:`share-seed-${workbook.id}`,
    cells:[{row:1,column:1,value:'항목'},{row:1,column:2,value:'금액'},{row:2,column:1,value:'임대료'},{row:2,column:2,value:1200}],
  }})
  return workbook
}

async function openEditor(page:Page,workbookId:string){
  await page.goto(`/workbooks/${workbookId}`)
  await page.waitForSelector('.grid-canvas,.access-denied')
  await page.waitForTimeout(800)
}

test('a restricted workbook offers the access request flow and the owner approves it', async ({ page, request, browser }) => {
  const workbook=await createWorkbook(request,`공유 정책 ${Date.now()}`)

  const guest=await pageAs(browser,'e2e-guest')
  await openEditor(guest.page,workbook.id)
  await expect(guest.page.getByRole('heading',{name:'액세스 권한이 필요합니다'})).toBeVisible()
  await guest.page.getByRole('button',{name:'편집 권한 요청'}).click()
  await expect(guest.page.getByRole('status')).toContainText('액세스 요청을 보냈습니다')

  await openEditor(page,workbook.id)
  await page.getByRole('button',{name:/공유/}).click()
  const dialog=page.getByRole('dialog',{name:/공유/})
  await expect(dialog).toContainText('대기 중인 액세스 요청 1건')
  await expect(dialog).toContainText('e2e-guest')
  await dialog.getByRole('button',{name:'승인'}).click()
  await expect(dialog).toContainText('e2e-guest에게 권한을 부여했습니다')

  await openEditor(guest.page,workbook.id)
  await expect(guest.page.locator('.grid-canvas')).toBeVisible()
  await guest.context.close()
})

test('a viewer sees a read-only editor and cannot write cells', async ({ page, request, browser }) => {
  const workbook=await createWorkbook(request,`뷰어 공유 ${Date.now()}`)
  const shared=await request.put(`/api/v1/workbooks/${workbook.id}/shares`,{data:{principal_type:'user',principal_id:'e2e-viewer',role:'viewer'}})
  expect(shared.ok()).toBeTruthy()

  const viewer=await pageAs(browser,'e2e-viewer')
  await openEditor(viewer.page,workbook.id)
  await expect(viewer.page.locator('.access-badge')).toContainText('보기 전용')
  await expect(viewer.page.getByRole('button',{name:'굵게'})).toBeDisabled()

  const canvas=viewer.page.locator('.grid-canvas')
  const box=await canvas.boundingBox()
  if(!box)throw new Error('grid canvas is not visible')
  await canvas.dblclick({position:{x:70,y:42}})
  await expect(viewer.page.locator('input.cell-editor')).toHaveCount(0)
  viewer.page.once('dialog',dialog=>dialog.accept())
  await viewer.page.keyboard.press('a')
  const cells=await request.get(`/api/v1/sheets/${workbook.sheets[0].id}/ranges/A1`).then(response=>response.json())
  expect(cells.items[0].value).toBe('항목')
  await viewer.context.close()

  // Promoting the same person to editor unlocks the toolbar.
  await request.put(`/api/v1/workbooks/${workbook.id}/shares`,{data:{principal_type:'user',principal_id:'e2e-viewer',role:'editor'}})
  const editor=await pageAs(browser,'e2e-viewer')
  await openEditor(editor.page,workbook.id)
  await expect(editor.page.locator('.access-badge')).toContainText('편집자')
  await expect(editor.page.getByRole('button',{name:'굵게'})).toBeEnabled()
  await editor.context.close()
  await page.goto('/')
})

test('sharing with a department reaches its members and the home page shows the source', async ({ page, request, browser }) => {
  const workbook=await createWorkbook(request,`부서 공유 ${Date.now()}`)
  const head=await request.post('/api/v1/departments',{data:{name:`경영지원본부 ${Date.now()}`}}).then(response=>response.json())
  const team=await request.post('/api/v1/departments',{data:{name:'재무팀',parent_id:head.id}}).then(response=>response.json())
  await request.post(`/api/v1/departments/${team.id}/members`,{data:{user_ids:['e2e-finance']}})
  await request.put(`/api/v1/workbooks/${workbook.id}/shares`,{data:{principal_type:'department',principal_id:head.id,principal_label:head.name,role:'commenter'}})

  const member=await pageAs(browser,'e2e-finance')
  await member.page.goto('/')
  await member.page.getByRole('button',{name:/나와 공유됨/}).click()
  await expect(member.page.locator('.workbook-card',{hasText:workbook.title}).locator('.workbook-access')).toContainText('댓글 작성자')
  await openEditor(member.page,workbook.id)
  await expect(member.page.locator('.access-badge')).toContainText('댓글 가능')
  await member.context.close()

  // The owner sees the shared department, which is the parent that the member
  // inherits the role from.
  await openEditor(page,workbook.id)
  await page.getByRole('button',{name:/공유/}).click()
  const dialog=page.getByRole('dialog',{name:/공유/})
  await expect(dialog).toContainText(head.name)
  await expect(dialog).toContainText('부서')
})

test('link access grants the whole organization the configured role', async ({ page, request, browser }) => {
  const workbook=await createWorkbook(request,`링크 공유 ${Date.now()}`)
  await openEditor(page,workbook.id)
  await page.getByRole('button',{name:/공유/}).click()
  const dialog=page.getByRole('dialog',{name:/공유/})
  await dialog.getByLabel('일반 액세스 범위').selectOption('organization')
  await expect(dialog.getByLabel('링크 액세스 권한')).toBeVisible()
  await dialog.getByRole('button',{name:'완료'}).click()

  const anyone=await pageAs(browser,'e2e-anyone')
  await openEditor(anyone.page,workbook.id)
  await expect(anyone.page.locator('.access-badge')).toContainText('보기 전용')
  await anyone.context.close()
})
