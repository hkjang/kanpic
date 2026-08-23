import { describe, expect, it } from 'vitest'
import { cellKey } from '../state/editor'
import { columnPages, printableDocument, usedRegion } from './printSheet'
import type { Cell } from '../types'

function grid(entries:Array<[number,number,unknown,Record<string,unknown>?]>){
  const cells=new Map<string,Cell>()
  for(const [row,column,value,style] of entries)cells.set(cellKey(row,column),{sheet_id:'sheet',row,column,value,style,updated_at:''})
  return cells
}

describe('usedRegion',()=>{
  it('bounds the non-empty cells and ignores blanks',()=>{
    const cells=grid([[2,3,'항목'],[5,4,12],[9,9,'']])
    expect(usedRegion(cells)).toEqual({startRow:2,startColumn:3,endRow:5,endColumn:4})
  })

  it('returns nothing for an empty sheet',()=>{
    expect(usedRegion(new Map())).toBeUndefined()
  })
})

describe('printableDocument',()=>{
  it('renders the used range with headers and cell styling',()=>{
    const cells=grid([[1,1,'매출',{bold:true,background:'#fef3c7'}],[1,2,1200,{horizontal_align:'right'}]])
    const html=printableDocument(cells,{title:'분기 보고',sheetName:'요약',gridlines:true,headers:true})
    expect(html).toContain('<title>분기 보고</title>')
    expect(html).toContain('요약 · A1:B1')
    expect(html).toContain('font-weight:700')
    expect(html).toContain('background:#fef3c7')
    expect(html).toContain('text-align:right')
    // Column and row headers appear when they are requested.
    expect(html).toContain('<th>A</th>')
    expect(html).toContain('<th class="row-head">1</th>')
  })

  it('escapes cell text so content cannot inject markup',()=>{
    const html=printableDocument(grid([[1,1,'<script>alert(1)</script>']]),{title:'제목',sheetName:'시트',gridlines:false,headers:false})
    expect(html).not.toContain('<script>alert')
    expect(html).toContain('&lt;script&gt;')
  })

  it('says so when there is nothing to print',()=>{
    expect(printableDocument(new Map(),{title:'빈 워크북',sheetName:'시트1',gridlines:true,headers:true})).toContain('인쇄할 데이터가 없습니다')
  })
})

describe('printing hidden rows',()=>{
  const sheet=()=>{
    const cells=new Map<string,Cell>()
    ;[['서울',100],['부산',50],['대구',30]].forEach(([name,amount],index)=>{
      cells.set(cellKey(index+1,1),{sheet_id:'s',row:index+1,column:1,value:name} as Cell)
      cells.set(cellKey(index+1,2),{sheet_id:'s',row:index+1,column:2,value:amount} as Cell)
    })
    return cells
  }

  it('leaves out the rows the reader cannot see',()=>{
    const html=printableDocument(sheet(),{title:'보고',sheetName:'요약',gridlines:true,headers:true,hiddenRows:new Set([2,3])})
    expect(html).toContain('서울')
    expect(html).not.toContain('부산')
    expect(html).not.toContain('대구')
  })

  it('keeps the original row numbers so the page matches the sheet',()=>{
    const html=printableDocument(sheet(),{title:'보고',sheetName:'요약',gridlines:true,headers:true,hiddenRows:new Set([2])})
    expect(html).toContain('<th class="row-head">3</th>')
    expect(html).not.toContain('<th class="row-head">2</th>')
  })

  it('prints everything when nothing is hidden',()=>{
    const html=printableDocument(sheet(),{title:'보고',sheetName:'요약',gridlines:true,headers:true})
    for(const name of ['서울','부산','대구'])expect(html).toContain(name)
  })
})

// 열 머리글이 표 본문의 첫 줄이면 첫 장에만 찍힌다. thead에 있어야 브라우저가
// 장마다 다시 그려 주고, 둘째 장부터도 어느 칸이 무슨 열인지 알 수 있다.
it('puts the column headings in a thead so every page repeats them',()=>{
  const cells=new Map<string,Cell>()
  for(let row=1;row<=3;row+=1)cells.set(cellKey(row,1),{sheet_id:'s',row,column:1,value:row,updated_at:''})
  const html=printableDocument(cells,{title:'긴 표',sheetName:'Sheet1',gridlines:true,headers:true})
  expect(html).toContain('<thead><tr><th class="corner"></th><th>A</th></tr></thead>')
  expect(html).toContain('<tbody>')
  // 본문 줄은 thead 밖에 있어야 한다.
  const head=html.slice(html.indexOf('<thead>'),html.indexOf('</thead>'))
  const body=html.slice(html.indexOf('<tbody>'))
  expect(body).toContain('<th class="row-head">1</th>')
  expect(head).not.toContain('row-head')
})

it('omits the thead when headings are turned off',()=>{
  const cells=new Map<string,Cell>()
  cells.set(cellKey(1,1),{sheet_id:'s',row:1,column:1,value:'값',updated_at:''})
  const html=printableDocument(cells,{title:'표',sheetName:'Sheet1',gridlines:false,headers:false})
  expect(html).not.toContain('<thead>')
  expect(html).toContain('<tbody>')
})


// 예전에는 표를 종이 폭에 맞춰 통째로 눌러 담았다. 열이 서른 개면 한 열이
// 몇 밀리미터로 찌그러져 읽을 수가 없었다. 엑셀은 들어가는 만큼 찍고 나머지를
// 다음 장으로 넘긴다.
describe('columnPages',()=>{
  it('keeps columns that fit on one page together',()=>{
    expect(columnPages(1,5,()=>100)).toHaveLength(1)
    expect(columnPages(1,5,()=>100)[0]).toMatchObject({start:1,end:5})
  })

  it('moves the columns that do not fit onto the next page',()=>{
    const pages=columnPages(1,20,()=>100)
    expect(pages.length).toBeGreaterThan(1)
    expect(pages[0].start).toBe(1)
    // 장과 장 사이에 빠지거나 겹치는 열이 없어야 한다.
    for(let at=1;at<pages.length;at+=1)expect(pages[at].start).toBe(pages[at-1].end+1)
    expect(pages[pages.length-1].end).toBe(20)
  })

  it('gives a column wider than the page a page of its own',()=>{
    const pages=columnPages(1,3,column=>column===2?2000:100)
    expect(pages.map(page=>[page.start,page.end])).toEqual([[1,1],[2,2],[3,3]])
  })

  it('follows the real width of each column',()=>{
    // 좁은 열은 한 장에 많이, 넓은 열은 적게 들어간다.
    expect(columnPages(1,12,()=>50).length).toBeLessThan(columnPages(1,12,()=>300).length)
  })
})

describe('printableDocument across pages',()=>{
  const wide=new Map<string,Cell>()
  for(let column=1;column<=12;column+=1)wide.set(cellKey(1,column),{sheet_id:'s',row:1,column,value:`열${column}`,updated_at:''})

  it('breaks a wide sheet into pages instead of squeezing it',()=>{
    const html=printableDocument(wide,{title:'넓은 표',sheetName:'시트1',gridlines:true,headers:true,columnWidth:()=>200})
    // 눌러 담지 않는다.
    expect(html).not.toContain('width:100%')
    expect(html.match(/<table>/g)?.length).toBeGreaterThan(1)
    // 어느 장에서든 어느 열의 몇 행인지 알 수 있어야 한다.
    expect(html.match(/<thead>/g)?.length).toBe(html.match(/<table>/g)?.length)
    expect(html.match(/class="row-head">1</g)?.length).toBe(html.match(/<table>/g)?.length)
    // 열은 하나도 빠지지 않는다.
    for(let column=1;column<=12;column+=1)expect(html).toContain(`열${column}`)
  })

  it('stays a single table when everything fits',()=>{
    const html=printableDocument(wide,{title:'좁은 표',sheetName:'시트1',gridlines:true,headers:true,columnWidth:()=>40})
    expect(html.match(/<table>/g)?.length).toBe(1)
  })
})

// 200행짜리 표를 인쇄하면 둘째 장부터는 어느 칸이 무슨 뜻인지 알 수 없었다.
// 화면에서 고정해 둔 행은 "여기까지가 머리글" 이라고 이미 말해 둔 것이다.
describe('repeating the frozen rows', ()=>{
  const long=new Map<string,Cell>()
  long.set(cellKey(1,1),{sheet_id:'s',row:1,column:1,value:'제품',updated_at:''})
  long.set(cellKey(1,2),{sheet_id:'s',row:1,column:2,value:'단가',updated_at:''})
  for(let row=2;row<=8;row+=1){
    long.set(cellKey(row,1),{sheet_id:'s',row,column:1,value:`품목${row}`,updated_at:''})
    long.set(cellKey(row,2),{sheet_id:'s',row,column:2,value:row*100,updated_at:''})
  }

  it('puts the frozen row in the head so every page carries it',()=>{
    const html=printableDocument(long,{title:'긴 표',sheetName:'시트1',gridlines:true,headers:true,frozenRows:1})
    const head=html.slice(html.indexOf('<thead>'),html.indexOf('</thead>'))
    expect(head).toContain('제품')
    expect(head).toContain('단가')
    // 머리글로 올라간 행이 본문에 한 번 더 나오면 안 된다.
    expect(html.match(/제품/g)).toHaveLength(1)
    expect(html).toContain('품목2')
  })

  it('leaves the body alone when nothing is frozen',()=>{
    const html=printableDocument(long,{title:'긴 표',sheetName:'시트1',gridlines:true,headers:true})
    const head=html.slice(html.indexOf('<thead>'),html.indexOf('</thead>'))
    expect(head).not.toContain('제품')
    expect(html).toContain('제품')
  })

  it('repeats the header on each column page too',()=>{
    const wide=new Map(long)
    for(let column=3;column<=12;column+=1)wide.set(cellKey(1,column),{sheet_id:'s',row:1,column,value:`열${column}`,updated_at:''})
    const html=printableDocument(wide,{title:'넓고 긴 표',sheetName:'시트1',gridlines:true,headers:true,frozenRows:1,columnWidth:()=>200})
    const tables=html.match(/<table>/g)?.length??0
    expect(tables).toBeGreaterThan(1)
    expect(html.match(/tr class="frozen"/g)?.length).toBe(tables)
  })

  it('does not repeat so many rows that no data fits',()=>{
    const html=printableDocument(long,{title:'긴 표',sheetName:'시트1',gridlines:true,headers:true,frozenRows:99})
    // 다섯 줄까지만 머리글로 올리고 나머지는 본문에 남는다.
    expect(html.match(/tr class="frozen"/g)).toHaveLength(5)
    expect(html).toContain('품목7')
  })

  it('leaves out a frozen row the reader cannot see',()=>{
    const html=printableDocument(long,{title:'긴 표',sheetName:'시트1',gridlines:true,headers:true,frozenRows:1,hiddenRows:new Set([1])})
    expect(html).not.toContain('제품')
    expect(html).toContain('품목2')
  })
})
