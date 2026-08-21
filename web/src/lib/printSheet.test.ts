import { describe, expect, it } from 'vitest'
import { cellKey } from '../state/editor'
import { printableDocument, usedRegion } from './printSheet'
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
