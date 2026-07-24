console.log("loaded app.js");

var app = angular.module('thermostatApp', ['ngWebSocket']);

// The UI is served under <prefix>/ui/, where <prefix> is empty when the daemon
// is reached directly and /api/hassio_ingress/<token> when it is reached
// through the Home Assistant ingress proxy.  Every request the page makes has
// to be built from that prefix -- hardcoded /api/... paths 404 behind ingress,
// which is why the add-on's ingress panel never worked.
function apiBase() {
  var path = window.location.pathname;
  var i = path.lastIndexOf('/ui/');
  if (i < 0) {
    // Served as .../ui with no trailing slash.
    i = path.lastIndexOf('/ui');
  }
  return (i >= 0 ? path.substring(0, i) : '') + '/api';
}

var API = apiBase();

app.factory('thermostatEvents', function ($websocket) {
        return {
            start: function (url, callback) {
                var s = $websocket(url, null, {reconnectIfNotNormalClose: true});
                s.onMessage(function(message) {
                  console.log("got message!");
        	  console.log(message);
                  callback(JSON.parse(message.data));
                });
            }
        }
    }
);

// Define the `PhoneListController` controller on the `phonecatApp` module
app.controller('thermostatController', function($scope, $http, $interval, thermostatEvents) {
  $scope.tstat = {};
  $scope.blower = {};

  // location.host already carries the port when there is one, and ingress
  // serves the page over the same scheme Home Assistant is using.
  var wsProto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  var $wsUrl = wsProto + "//" + window.location.host + API + "/ws";

  thermostatEvents.start($wsUrl, function (msg) {
    if (msg.source == "tstat") {
       $scope.tstat = msg.data;
    } else if (msg.source == "blower") {
       $scope.blower = msg.data;
    }
  });

  // $scope.events = thermostatEvents;

  $scope.refreshState = function () {
     $http.get(API + "/zone/1/config").then(function(response) {
      $scope.tstat = response.data;
    });
  };

  $scope.setFanSpeed = function(speed) {
    $http.put(API + "/zone/1/config", { "fanMode": speed }).then(function(response) {
      console.log("set fan speed") ;
    });
  }

  $scope.setMode = function(mode) {
    $http.put(API + "/zone/1/config", { "mode": mode }).then(function(response) {
      console.log("set mode") ;
    });
  }

  $scope.setHold = function(hold) {
    $http.put(API + "/zone/1/config", { "hold": hold }).then(function(response) {
      console.log("set hold") ;
    });
  }

  $scope.incCoolSetpoint = function(val) {
    var temp = $scope.tstat.coolSetpoint + val;
    $http.put(API + "/zone/1/config", { "coolSetpoint": temp }).then(function(response) {
      console.log("set cool setpoint") ;
    });
  }

  $scope.incHeatSetpoint = function(val) {
    var temp = $scope.tstat.heatSetpoint + val;
    $http.put(API + "/zone/1/config", { "heatSetpoint": temp }).then(function(response) {
      console.log("set heat setpoint") ;
    });
  }

/*
  $interval(function () {
     $scope.refreshState();
  }, 1000);
*/
});
