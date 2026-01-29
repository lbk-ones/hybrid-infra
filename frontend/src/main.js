import {Alert, Confirm, OpenWindowByUrl, Show} from "../bindings/hybrid-infra/go/services/uiservice.js";


window.onload = function () {
    var elementById = document.getElementById("alert");
    elementById.addEventListener("click", function (e) {
        Alert("测试标题ggg", "5秒后<span style='color:red'>6</span>自动关闭", 2)
    })
    document.getElementById("confirm")
        .addEventListener("click", async function (e) {
            let res = await Confirm({
                title: "这是一个标题",
                body: "这是一个body<span style='color:red'>6</span>",
                okText: "ok",
                cancelText: 'cancel'
            })
            alert(res);
        })

    document.getElementById("show")
        .addEventListener("click",  function (e) {
            let a = ['center','bottomLeft','bottomRight','topLeft','topRight']
            a.forEach(async w=>{
                await Show({
                    body: `<div style="font-size:14px; color:#1A1A1A;padding:10px;">张 三 <span style="margin-left:10px;color:blue;">男</span> | 63岁 <a style="margin-left:25px; color:#1978D4; text-decoration:none;" onclick="closeWindow()">查看详情</a></div> `,
                    place: w,
                    width: 300,
                    height: 100
                })
            })
        })

    document.getElementById("openBaidu")
        .addEventListener("click",  function (e) {
            OpenWindowByUrl({
                url:"https://www.baidu.com",
                isFullscreen:true,
                title:  "这是百度",
                alwaysOnTop:true,
                hiddenOnTaskbar:true,
                place:"center"
            })
        })
}