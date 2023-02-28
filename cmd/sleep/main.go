/*
Copyright 2021 The actions-runner-controller authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"time"
)

var Seconds int

func main() {
	fmt.Printf("sleeping for %d seconds\n", Seconds)
	time.Sleep(time.Duration(Seconds) * time.Second)
	fmt.Println("done sleeping")
}

func init() {
	flag.IntVar(&Seconds, "seconds", 60, "Number of seconds to sleep")
	flag.Parse()
}
