import { expect, test } from '@playwright/test'

// 인쇄물은 셀마다 다른 색과 굵기를 style 속성으로 싣는다. 그런데 앱의 보안
// 정책은 인라인 스타일을 막으므로, 빈 프레임에 그냥 쓰면 그 전부가 조용히
// 버려지고 글자만 남은 종이가 나온다.
//
// 이 시험이 없던 동안 인쇄물에는 색도 굵기도 테두리도 없었다. 인쇄 시험들이
// 글자만 확인했기 때문이다. 그리는 것이 일인 기능은 그려졌는지를 봐야 한다.
test('a printed sheet keeps the colours and weights it was given', async ({ page, request }) => {
  const stamp=Date.now()
  const workbook=await request.post('/api/v1/workbooks',{data:{title:`인쇄 서식 ${stamp}`}}).then(r=>r.json())
  const sheet=workbook.sheets[0].id as string
  await request.patch(`/api/v1/sheets/${sheet}/cells:batch`,{data:{idempotency_key:`style-${stamp}`,cells:[
    {row:1,column:1,value:'강조',style:{background:'#ff0000',color:'#ffffff',bold:true}},
    {row:2,column:1,value:'보통'},
  ]}})
  await page.goto(`/workbooks/${workbook.id}`)
  await expect(page.locator('.grid-canvas')).toBeVisible()
  // 인쇄 대화 상자를 띄우지 않고 문서만 만든다.
  await page.evaluate(()=>{(window as unknown as {print:()=>void}).print=()=>{}})
  await page.getByRole('menubar',{name:'워크북 메뉴'}).getByRole('menuitem',{name:'파일',exact:true}).click()
  await page.getByRole('menuitem',{name:'인쇄'}).click()

  const painted=await (async()=>{
    for(let attempt=0;attempt<50;attempt+=1){
      const found=await page.evaluate(()=>{
        for(const frame of [...document.querySelectorAll('iframe')]){
          const doc=frame.contentDocument
          const view=doc?.defaultView
          if(!doc||!view)continue
          const cell=[...doc.querySelectorAll('td')].find(item=>item.textContent?.includes('강조'))
          const table=doc.querySelector('table')
          if(!cell||!table)continue
          return {
            background:view.getComputedStyle(cell).backgroundColor,
            weight:view.getComputedStyle(cell).fontWeight,
            // 문서가 실은 <style> 블록도 적용되어야 표가 표처럼 나온다.
            collapse:view.getComputedStyle(table).borderCollapse,
          }
        }
        return undefined
      })
      if(found)return found
      await page.waitForTimeout(100)
    }
    throw new Error('the print document never appeared')
  })()

  expect(painted.background).toBe('rgb(255, 0, 0)')
  expect(painted.weight).toBe('700')
  expect(painted.collapse).toBe('collapse')
  await request.delete(`/api/v1/workbooks/${workbook.id}`)
})

// 인쇄 문서에만 스타일을 허용한 것이지 스크립트를 허용한 것이 아니다.
test('the print page allows styling and nothing else', async ({ request }) => {
  const response=await request.get('/print-frame')
  expect(response.status()).toBe(200)
  const policy=response.headers()['content-security-policy']??''
  expect(policy).toContain("default-src 'none'")
  expect(policy).toContain("style-src 'unsafe-inline'")
  // 스크립트도, 바깥으로 나가는 연결도 허용하지 않는다.
  expect(policy).not.toContain('script-src')
  expect(policy).not.toContain('connect-src')
})
