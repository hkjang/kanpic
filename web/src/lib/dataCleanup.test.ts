import { describe, expect, it } from 'vitest'
import { cellKey } from '../state/editor'
import { detectDelimiter, removeDuplicateRows, splitLine, splitTextToColumns, splitWouldOverwrite, trimWhitespace } from './dataCleanup'
import type { Cell } from '../types'

function grid(entries:Array<[number,number,unknown]>){
  const cells=new Map<string,Cell>()
  for(const [row,column,value] of entries)cells.set(cellKey(row,column),{sheet_id:'sheet',row,column,value,updated_at:''})
  return cells
}
const region=(startRow:number,startColumn:number,endRow:number,endColumn:number)=>({startRow,startColumn,endRow,endColumn})

describe('removeDuplicateRows',()=>{
  it('keeps the first occurrence and compacts the rows below it',()=>{
    const cells=grid([[1,1,'이름'],[1,2,'부서'],[2,1,'박지민'],[2,2,'영업'],[3,1,'박지민'],[3,2,'영업'],[4,1,'이서준'],[4,2,'개발']])
    const result=removeDuplicateRows(cells,region(1,1,4,2),1)
    expect(result.removed).toBe(1)
    // The unique third row moves up into the duplicate's place.
    expect(result.writes).toContainEqual({row:3,column:1,value:'이서준',formula:undefined,style:undefined})
    // The vacated last row is emptied instead of keeping a stale copy.
    expect(result.writes).toContainEqual({row:4,column:1,value:undefined,formula:undefined,style:undefined})
  })

  it('reports nothing to do when every row is unique',()=>{
    const cells=grid([[1,1,'가'],[2,1,'나']])
    expect(removeDuplicateRows(cells,region(1,1,2,1),0)).toEqual({writes:[],removed:0})
  })
})

describe('trimWhitespace',()=>{
  it('trims edges, collapses inner runs and leaves formulas alone',()=>{
    const cells=grid([[1,1,'  박지민  '],[2,1,'영업   1팀'],[3,1,'정상']])
    cells.set(cellKey(4,1),{sheet_id:'sheet',row:4,column:1,formula:'=A1',value:' 값 ',updated_at:''})
    const result=trimWhitespace(cells,region(1,1,4,1))
    expect(result.changed).toBe(2)
    expect(result.writes[0].value).toBe('박지민')
    expect(result.writes[1].value).toBe('영업 1팀')
  })
})

describe('splitTextToColumns',()=>{
  it('splits on the detected delimiter and keeps numbers numeric',()=>{
    const cells=grid([[1,1,'박지민,영업,1200'],[2,1,'이서준,개발,980']])
    const result=splitTextToColumns(cells,region(1,1,2,1),'auto')
    expect(result.separator).toBe(',')
    expect(result.columns).toBe(3)
    expect(result.writes).toContainEqual({row:1,column:3,value:1200,formula:undefined,style:undefined})
    expect(result.writes).toContainEqual({row:2,column:2,value:'개발',formula:undefined,style:undefined})
  })

  it('does nothing when the delimiter never appears',()=>{
    const cells=grid([[1,1,'박지민'],[2,1,'이서준']])
    expect(splitTextToColumns(cells,region(1,1,2,1),',').columns).toBe(0)
  })

  it('detects the delimiter that appears in the most rows',()=>{
    expect(detectDelimiter(['a;b','c;d','e f'])).toBe(';')
  })

  it('warns before overwriting neighbouring data',()=>{
    const cells=grid([[1,1,'a,b'],[1,2,'기존']])
    expect(splitWouldOverwrite(cells,region(1,1,1,1),2)).toBe(true)
    expect(splitWouldOverwrite(cells,region(1,1,1,1),1)).toBe(false)
  })
})

describe('splitLine',()=>{
  it('keeps a quoted field together',()=>{
    expect(splitLine('"서울, 강남",100,완료',',')).toEqual(['서울, 강남','100','완료'])
  })

  it('reads a doubled quote inside a quoted field as one quote',()=>{
    expect(splitLine('"그는 ""안녕"" 이라고",2',',')).toEqual(['그는 "안녕" 이라고','2'])
  })

  it('leaves quotes that are part of the text alone',()=>{
    expect(splitLine('15" 모니터,3',',')).toEqual(['15" 모니터','3'])
  })

  it('splits on a multi-character separator',()=>{
    expect(splitLine('서울 :: 100 :: 완료',' :: ')).toEqual(['서울','100','완료'])
  })
})
