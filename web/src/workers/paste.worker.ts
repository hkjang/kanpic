/// <reference lib="webworker" />
self.onmessage = (event: MessageEvent<{text:string;startRow:number;startColumn:number}>) => {
  const {text,startRow,startColumn}=event.data
  const cells: Array<{row:number;column:number;value:unknown}> = []
  text.replace(/\r\n/g,'\n').replace(/\r/g,'\n').split('\n').forEach((line,rowOffset)=>{
    if(rowOffset===text.split(/\r?\n/).length-1 && line==='')return
    line.split('\t').forEach((raw,columnOffset)=>{
      let value:unknown=raw
      if(raw!=='' && Number.isFinite(Number(raw)))value=Number(raw)
      else if(raw.toLowerCase()==='true'||raw.toLowerCase()==='false')value=raw.toLowerCase()==='true'
      cells.push({row:startRow+rowOffset,column:startColumn+columnOffset,value})
    })
  })
  self.postMessage(cells)
}
